package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
)

// "提交结果未确认"是指请求已经发出、但平台没拿到上游回执的状态：上游可能已经建了任务并
// 开始计费，也可能根本没收到。这类任务的预扣按 taskRefundVerdict 的既定判据不退——请求
// 发出去了却查不出结果，恰恰是最可能已经计费的状态，随手退就是把成本挪给平台。
//
// 但"查不出结果"不该是终局：用户既拿不到产物、也看不到钱。所以这里补两步：
//
//  1. 自动对账。多数回执丢失其实能补救——上游任务号就留在那次调用的请求日志里。捞到就
//     替用户去问一次上游：出了片就把产物取回来登记，上游明确失败或取消就退预扣。
//  2. 到期兜底。宽限期内始终拿不到可查的上游任务，说明这条提交既问不出结果、也不会再
//     回来，按"无产出"退回预扣，并把理由写进任务日志和积分明细。
//
// 判据没有放宽：只要查得到上游任务，定性就交给上游，平台不猜。
const (
	// 首次对账前的等待。阻塞式创建接口的慢请求就在分钟级，太早问只会空手而归。
	providerSubmissionReconcileGrace = 2 * time.Minute
	// 单条任务两次对账之间的间隔。到期前每条任务大约被问 48 次。
	providerSubmissionReconcileInterval = 30 * time.Minute
	// 拿不到可查的上游任务时的兜底期限：超过它就认定这次提交没有产出，退回预扣。
	providerSubmissionReconcileRefundAfter = 24 * time.Hour
	// 拿得到上游任务、但上游长期不出结果时的移交点：超过它转人工，
	// 不再让一条迟迟不终态的任务一直占着对账名额。
	providerSubmissionReconcileHandoffAfter = 7 * 24 * time.Hour
	// 扫描节拍。真正决定"什么时候再问一次"的是每条任务的 next_poll_at。
	providerSubmissionReconcileTick  = 5 * time.Minute
	providerSubmissionReconcileLease = 10 * time.Minute
	// 单轮最多处理多少条，避免堆积时在一轮里跑太久。
	providerSubmissionReconcileBatch = 20
)

// submissionReconciledPollStage 标记"这条任务的提交结果已经结清"。
//
// 结清后任务仍留在失败态等用户自己重试，因此不能靠状态位区分；没有这个标记，下一轮扫描
// 会把同一条任务反复捞起来。退款本身由账号域的幂等键兜底，这个标记省掉的是无效轮询。
const submissionReconciledPollStage = "unreconciled"

const (
	unreconciledSubmissionRefundNote  = "上游提交结果长期未确认，按无产出退回预扣"
	unreconciledSubmissionMessage     = "提交结果始终未确认，上游也查不到对应的任务，本次消耗的积分已退回。可以重新提交一次。"
	providerNoOutputSubmissionNote    = "上游确认这次生成没有产出，退回预扣"
	providerNoOutputSubmissionMessage = "上游已终止这次生成且没有产出，本次消耗的积分已退回。"
	providerSubmissionHandoffMessage  = "上游任务长时间没有出片，已转人工跟进。"
)

func (s *Service) startProviderSubmissionReconciliation(ctx context.Context) {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(providerSubmissionReconcileTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for claimed := 0; claimed < providerSubmissionReconcileBatch; claimed++ {
				if ctx.Err() != nil {
					return
				}
				owner := providerRecoveryLeaseOwnerPrefix + s.workerID
				task, err := s.repo.ClaimNextSubmissionUncertainTask(owner, providerSubmissionReconcileLease, time.Now().Add(-providerSubmissionReconcileGrace))
				if err != nil {
					log.Printf("submission reconciliation paused: stage=claim worker_id=%s error=%v", s.workerID, err)
					break
				}
				if task == nil {
					break
				}
				reconcileCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				reconcileErr := s.reconcileSubmissionUncertainTask(reconcileCtx, task)
				cancel()
				if reconcileErr != nil {
					// 一次对账失败不能拖着租约不放：记下来，排到下一轮再问。
					_ = s.log(task.UserID, task.ID, "error", "提交结果对账失败", reconcileErr.Error())
					if deferErr := s.deferSubmissionReconciliation(task); deferErr != nil {
						_ = s.log(task.UserID, task.ID, "error", "提交结果对账排期失败", deferErr.Error())
					}
				}
				// 结清路径已经在写结论时释放了租约，这里是幂等的补救：恢复成功那条路
				// 走的是任务成功收尾，不会自己清租约。
				_ = s.releaseSubmissionReconciliation(task)
			}
		}
	})
}

// reconcileSubmissionUncertainTask 对一条"提交结果未确认"的任务做一次对账。
//
// 调用方已持有租约。结论无非三种：恢复到产物、按上游回执结清、或按无产出结清；都不是的
// （上游仍在处理、查询临时失败）留给下一轮。
func (s *Service) reconcileSubmissionUncertainTask(ctx context.Context, task *model.Task) error {
	if task == nil || task.ID == "" {
		return nil
	}
	if task.PollStage == submissionReconciledPollStage {
		return s.releaseSubmissionReconciliation(task)
	}
	recovery, err := s.prepareProviderVideoRecovery(ctx, task)
	if err != nil {
		// 问不了上游（没有任务号、或该协议不支持安全查询）不是临时故障，等多久都一样，
		// 直接走到期兜底；其余错误多半是配置读取失败，留给下一轮。
		var unavailable providerRecoveryUnavailableError
		if !errors.As(err, &unavailable) {
			return err
		}
		return s.settleUnreconciledSubmission(task)
	}
	return s.reconcileSubmissionAgainstProvider(ctx, task, recovery)
}

// reconcileSubmissionAgainstProvider 用上游的结果给这条任务定性。
func (s *Service) reconcileSubmissionAgainstProvider(ctx context.Context, task *model.Task, recovery providerVideoRecovery) error {
	if time.Since(task.CreatedAt) > providerSubmissionReconcileHandoffAfter {
		return s.handOffSubmissionReconciliation(task)
	}
	result, err := s.runProviderVideoRecovery(ctx, task, recovery, "自动对账")
	if err == nil {
		if result != nil && result.Recovered {
			return nil
		}
		// 上游仍在处理：下轮再问。
		return s.deferSubmissionReconciliation(task)
	}
	// 上游明确回了失败或取消，说明这次生成确实没有产出：按既有判据退款，
	// 与执行期失败的收尾保持一致。
	if refundable, _ := s.taskRefundVerdict(task, err); refundable {
		if markErr := s.markSubmissionReconciled(task, "任务失败", providerNoOutputSubmissionMessage); markErr != nil {
			return markErr
		}
		task.Stage = "任务失败"
		s.refundTaskCredits(task, err, providerNoOutputSubmissionNote)
		return nil
	}
	// 查一次失败可能只是网络或上游抖动，任务本身还没结论，留给下一轮。
	return s.deferSubmissionReconciliation(task)
}

// settleUnreconciledSubmission 处理"平台手里没有可查的上游任务"这种情形。
func (s *Service) settleUnreconciledSubmission(task *model.Task) error {
	if time.Since(task.CreatedAt) < providerSubmissionReconcileRefundAfter {
		return s.deferSubmissionReconciliation(task)
	}
	// 先结清再退钱：结清会释放租约并留下标记，否则下一轮扫描会把同一条任务再捞一遍。
	// 退款本身可重放，账号域按 (任务, 退回) 的唯一键去重。
	if err := s.markSubmissionReconciled(task, "任务失败", unreconciledSubmissionMessage); err != nil {
		return err
	}
	task.Stage = "任务失败"
	task.Error = unreconciledSubmissionMessage
	s.refundTaskCreditsUnchecked(task, unreconciledSubmissionRefundNote)
	_ = s.log(task.UserID, task.ID, "warn", "提交结果长期未确认，已按无产出退回预扣", "")
	return nil
}

// handOffSubmissionReconciliation 记下"自动对账已用尽"并交还人工。
//
// 这条任务在上游是活的，只是迟迟不出片，所以保留原来的阶段：重试仍然要挡着，
// 否则用户重发一次就是又花一笔上游的钱。
func (s *Service) handOffSubmissionReconciliation(task *model.Task) error {
	if err := s.repo.UpdateTaskProviderState(task.ID, "", submissionReconciledPollStage, nil); err != nil {
		return err
	}
	_ = s.log(task.UserID, task.ID, "warn", providerSubmissionHandoffMessage, "")
	return s.releaseSubmissionReconciliation(task)
}

// markSubmissionReconciled 写入对账结论、停掉后续对账并释放租约。
func (s *Service) markSubmissionReconciled(task *model.Task, stage string, message string) error {
	return s.repo.MarkTaskSubmissionReconciled(task.ID, task.LeaseOwner, stage, message, submissionReconciledPollStage)
}

func (s *Service) deferSubmissionReconciliation(task *model.Task) error {
	next := time.Now().Add(providerSubmissionReconcileInterval)
	if task.NextPollAt == nil {
		_ = s.log(task.UserID, task.ID, "warn", "提交结果未确认，开始自动对账", "")
	}
	return s.repo.UpdateTaskProviderState(task.ID, "", task.PollStage, &next)
}

func (s *Service) releaseSubmissionReconciliation(task *model.Task) error {
	if task == nil || strings.TrimSpace(task.LeaseOwner) == "" {
		return nil
	}
	return s.repo.ReleaseTaskProviderRecovery(task.ID, task.LeaseOwner)
}
