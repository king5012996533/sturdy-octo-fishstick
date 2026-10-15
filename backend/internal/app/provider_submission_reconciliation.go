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
//  2. 无产出结清。始终拿不到可查的上游任务，说明这条提交既问不出结果、也不会再回来，
//     按"无产出"退回预扣，并把理由写进任务日志和积分明细。用户没拿到产物就不该为它付费，
//     而拿不到任务号就等于没有产物可领——所以这条兜底只留"让写入次序自证"的时间，不按天等。
//  3. 长跑结清。上游任务查得到、但问到期限还是一句话"在跑"，产物同样不会落地。这时
//     退款并按无产出结清，平台为一条交不出货的上游任务垫付是平台自己的成本。
//
// 判据没有放宽：只要查得到上游任务，定性就交给上游，平台不猜。
const (
	// 首次对账前的等待。阻塞式创建接口的慢请求就在分钟级，太早问只会空手而归。
	providerSubmissionReconcileGrace = 2 * time.Minute
	// 单条任务两次对账之间的间隔。重复问只对"上游仍在处理"的情形有意义。
	providerSubmissionReconcileInterval = 30 * time.Minute
	// 拿不到可查的上游任务时的兜底期限：超过它就认定这次提交没有产出，退回预扣。
	//
	// 上游任务号只可能出现在提交当次写下的请求日志里：捞不到就永远捞不到，产物也不会自己
	// 回来。所以期限只需要覆盖"日志比终态晚一步落库"这种次序问题——按天等既不改变结论，
	// 又让用户在拿不到产物之后还要多等一天才拿回积分。
	providerSubmissionReconcileRefundAfter = 5 * time.Minute
	// 拿得到上游任务、但上游长期不出结果的结清点：超过它按无产出退款，并停止自动对账。
	//
	// 上游那条任务可能还活着，但从提交那一刻起用户什么都没拿到，再等也不会有产物：留下来的
	// 只有"平台要不要继续替上游垫这笔钱"的问题。期限给到天级是为了容下真正的长尾任务，
	// 不是为了把一条交不出货的单子一直挂在用户账上。
	providerSubmissionReconcileNoOutputAfter = 7 * 24 * time.Hour
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
	unreconciledSubmissionRefundNote   = "上游查不到这次提交的任务，按无产出退回预扣"
	unreconciledSubmissionMessage      = "提交结果始终未确认，上游也查不到对应的任务，本次消耗的积分已退回。可以重新提交一次。"
	providerNoOutputSubmissionNote     = "上游确认这次生成没有产出，退回预扣"
	providerNoOutputSubmissionMessage  = "上游已终止这次生成且没有产出，本次消耗的积分已退回。"
	providerNoOutputLongRunningNote    = "上游任务长期没有出片，按无产出退回预扣"
	providerNoOutputLongRunningMessage = "上游任务长时间没有出片，本次消耗的积分已退回。可以重新提交一次。"
	providerSubmissionHandoffMessage   = "上游任务长时间没有出片，已转人工跟进。"
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
	// 到期也先问最后一次：产物可能刚好在这一刻才可领，把"还没来得及取"当成"没有产出"
	// 退钱，用户就白丢了一条本可以拿到的片子。
	expired := time.Since(task.CreatedAt) > providerSubmissionReconcileNoOutputAfter
	result, err := s.runProviderVideoRecovery(ctx, task, recovery, "自动对账")
	if err == nil {
		if result != nil && result.Recovered {
			return nil
		}
		// 上游仍在处理：到期就按无产出结清，否则下轮再问。
		if expired {
			return s.settleLongRunningSubmission(task)
		}
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
	// 这次问不出结论（网络抖动、产物没接回来等）：到期把这条任务交还人工，但不按无产出
	// 退钱——"有产物却没接回来"与"上游交不出货"是两件事，判据不能混。其余留给下一轮。
	if expired {
		return s.handOffSubmissionReconciliation(task)
	}
	return s.deferSubmissionReconciliation(task)
}

// settleUnreconciledSubmission 处理"平台手里没有可查的上游任务"这种情形。
func (s *Service) settleUnreconciledSubmission(task *model.Task) error {
	// 期限之前再问一次没有意义：平台手里没有上游任务号这件事不会随时间改变。这里只把
	// 下一次对账压到期限上，不排满一整个间隔——否则期限前的一次领取会把退款整整推后一轮。
	if remaining := providerSubmissionReconcileRefundAfter - time.Since(task.CreatedAt); remaining > 0 {
		return s.deferSubmissionReconciliationAt(task, time.Now().Add(min(remaining, providerSubmissionReconcileInterval)))
	}
	// 先结清再退钱：结清会释放租约并留下标记，否则下一轮扫描会把同一条任务再捞一遍。
	// 退款本身可重放，账号域按 (任务, 退回) 的唯一键去重。
	if err := s.markSubmissionReconciled(task, "任务失败", unreconciledSubmissionMessage); err != nil {
		return err
	}
	task.Stage = "任务失败"
	task.Error = unreconciledSubmissionMessage
	s.refundTaskCreditsUnchecked(task, unreconciledSubmissionRefundNote)
	_ = s.log(task.UserID, task.ID, "warn", "上游查不到这次提交的任务，已按无产出退回预扣", "")
	return nil
}

// settleLongRunningSubmission 处理"上游任务确实存在，但问到期限还是没有产物"这种情形。
//
// 上游那条任务可能还活着、也还在计费，但用户从头到尾什么都没拿到：等待不产生价值，只有
// 平台在为一条交不出货的上游任务垫钱。所以这里按无产出结清并退回预扣，同时留下"转人工"
// 的日志，让上游账单对账时能看出这笔钱是平台自己吃的。
func (s *Service) settleLongRunningSubmission(task *model.Task) error {
	if err := s.markSubmissionReconciled(task, "任务失败", providerNoOutputLongRunningMessage); err != nil {
		return err
	}
	task.Stage = "任务失败"
	task.Error = providerNoOutputLongRunningMessage
	s.refundTaskCreditsUnchecked(task, providerNoOutputLongRunningNote)
	_ = s.log(task.UserID, task.ID, "warn", providerSubmissionHandoffMessage, "")
	return nil
}

// handOffSubmissionReconciliation 记下"自动对账已用尽"并交还人工。
//
// 走到这里的任务上游查得到、产物可能真实存在，只是这次问不出结论（网络抖动、下载失败）：
// 因此保留原来的阶段与预扣，重试也要继续挡着——用户手一抖重发就是又花一笔上游的钱。
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
	return s.deferSubmissionReconciliationAt(task, time.Now().Add(providerSubmissionReconcileInterval))
}

// deferSubmissionReconciliationAt 把这条任务排到指定时刻再对账。
//
// 调用方可能是"再说一遍也无妨"的上游仍在处理，也可能是"到点就该结清"的无产出兜底：
// 后者的排期必须由结算期限决定，不能被固定间隔顶掉。
func (s *Service) deferSubmissionReconciliationAt(task *model.Task, at time.Time) error {
	if task.NextPollAt == nil {
		_ = s.log(task.UserID, task.ID, "warn", "提交结果未确认，开始自动对账", "")
	}
	return s.repo.UpdateTaskProviderState(task.ID, "", task.PollStage, &at)
}

func (s *Service) releaseSubmissionReconciliation(task *model.Task) error {
	if task == nil || strings.TrimSpace(task.LeaseOwner) == "" {
		return nil
	}
	return s.repo.ReleaseTaskProviderRecovery(task.ID, task.LeaseOwner)
}
