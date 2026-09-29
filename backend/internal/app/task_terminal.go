package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// taskTerminalCoordinator 收敛任务进入终态后的业务策略。
//
// 任务执行本身仍由 Service 编排，但失败、取消和成功收尾必须保持一致，
// 不能散落在 provider/worker 分支中。
type taskTerminalCoordinator struct {
	repo              taskTerminalRepository
	replay            taskReplayLifecycle
	logger            taskLifecycleLogger
	outputs           taskOutputLifecycle
	refund            taskRefundLifecycle
	userFacingMessage func(error) string
	logFailedAttempt  func(model.Task, error)
}

type taskTerminalRepository interface {
	Task(id string) (*model.Task, error)
	UpdateTaskTerminalState(id string, owner string, expected model.TaskStatus, status model.TaskStatus, stage string, errorText string, completedAt time.Time) (bool, error)
}

type taskReplayLifecycle interface {
	finalizeTaskTextReplay(taskID string, status model.TaskStatus) error
}

type taskLifecycleLogger interface {
	log(userID string, taskID string, level string, message string, payload string) error
}

type taskOutputLifecycle interface {
	RegisterTaskOutputFromTask(task model.Task) error
}

// taskRefundLifecycle 是终态路径退回预扣的窄端口。
//
// 单独一个接口而不是并进 taskLifecycleLogger：写日志是纯观测，退回是资金动作，
// 让两者的实现被迫同时存在，会让"只想记日志"的测试替身不得不假装能退钱。
type taskRefundLifecycle interface {
	refundTaskCredits(userID string, taskID string, note string)
}

type taskTerminalServiceAdapter struct {
	finalizeReplay func(string, model.TaskStatus) error
	writeLog       func(string, string, string, string, string) error
	registerOutput func(model.Task) error
	refundCredits  func(string, string, string)
}

func (a taskTerminalServiceAdapter) finalizeTaskTextReplay(taskID string, status model.TaskStatus) error {
	return a.finalizeReplay(taskID, status)
}

func (a taskTerminalServiceAdapter) log(userID string, taskID string, level string, message string, payload string) error {
	return a.writeLog(userID, taskID, level, message, payload)
}

func (a taskTerminalServiceAdapter) RegisterTaskOutputFromTask(task model.Task) error {
	return a.registerOutput(task)
}

// refundTaskCredits 是 taskRefundLifecycle 的适配入口。
//
// 函数为 nil 时直接返回：桌面与本地装载没有计费端口，那里不该为"退不了钱"报错。
func (a taskTerminalServiceAdapter) refundTaskCredits(userID string, taskID string, note string) {
	if a.refundCredits == nil {
		return
	}
	a.refundCredits(userID, taskID, note)
}

func newTaskTerminalCoordinator(s *Service) *taskTerminalCoordinator {
	adapter := taskTerminalServiceAdapter{
		finalizeReplay: s.finalizeTaskTextReplay,
		writeLog:       s.log,
		registerOutput: s.RegisterTaskOutputFromTask,
		refundCredits:  s.refundTaskCredits,
	}
	return &taskTerminalCoordinator{
		repo:              s.repo,
		replay:            adapter,
		logger:            adapter,
		outputs:           adapter,
		refund:            adapter,
		userFacingMessage: s.UserFacingErrorMessage,
		logFailedAttempt:  s.ensureFailedProviderAttemptLogged,
	}
}

func (s *Service) terminalCoordinator() *taskTerminalCoordinator {
	if s.taskTerminalCoordinator != nil {
		return s.taskTerminalCoordinator
	}
	// 部分单元测试直接构造 Service 字面量；延迟创建保持这些测试和内部工具兼容。
	return newTaskTerminalCoordinator(s)
}

func (c *taskTerminalCoordinator) markPreparationFailure(task *model.Task, stage string, err error, _ bool, _ string) error {
	c.ensureFailedAttemptLogged(task, err)
	task.Status = model.TaskStatusFailed
	task.Stage = stage
	task.Error = c.userFacingMessage(err)
	if terminalErr := c.markTerminalState(task); terminalErr != nil {
		return errors.Join(err, terminalErr)
	}
	return err
}

// handleExecutionFailure 返回 nil 仅表示取消已被正常收尾；普通失败仍返回原始错误，
// 让 worker 保留重试/监控所需的失败语义。
func (c *taskTerminalCoordinator) handleExecutionFailure(task *model.Task, err error, providerSucceeded bool, channelSlotFailedBeforeRequest bool) error {
	if errors.Is(err, context.Canceled) {
		// 用户取消会先把数据库任务置为 cancelled，再停止 worker context。
		// 此时不再重复退款/核对，只补齐 worker 侧的回放和日志收尾。
		if latest, latestErr := c.repo.Task(task.ID); latestErr == nil && latest.Status == model.TaskStatusCancelled {
			return c.handleAlreadyCancelled(*latest)
		}
		task.Status = model.TaskStatusCancelled
		task.Stage = "任务已取消"
		task.Error = "任务已取消"
		if terminalErr := c.markTerminalState(task); terminalErr != nil {
			return terminalErr
		}
		c.finalizeReplay(task, model.TaskStatusCancelled, "文本回放草稿归并失败")
		_ = c.logger.log(task.UserID, task.ID, "warn", "任务已取消", "")
		return nil
	}

	task.Status = model.TaskStatusFailed
	c.ensureFailedAttemptLogged(task, err)
	task.Stage = "任务失败"
	task.Error = c.userFacingMessage(err)
	if terminalErr := c.markTerminalState(task); terminalErr != nil {
		return errors.Join(err, terminalErr)
	}
	c.finalizeReplay(task, model.TaskStatusFailed, "文本回放草稿归并失败")
	_ = c.logger.log(task.UserID, task.ID, "error", "任务处理失败", task.Error)
	return err
}

func (c *taskTerminalCoordinator) handleAlreadyCancelled(task model.Task) error {
	c.finalizeReplay(&task, model.TaskStatusCancelled, "文本回放草稿归并失败")
	_ = c.logger.log(task.UserID, task.ID, "warn", "任务已取消，worker 已停止执行", "")
	return nil
}

func (c *taskTerminalCoordinator) handleCancelledResult(task model.Task) error {
	c.finalizeReplay(&task, model.TaskStatusCancelled, "文本回放草稿归并失败")
	_ = c.logger.log(task.UserID, task.ID, "warn", "任务已取消，丢弃生成结果", "")
	return nil
}

// handleResultPersistenceFailure 处理“上游成功但本地结果保存失败”的收尾。
// 返回 handled=true 表示并发取消已被识别并正常收尾，调用方不应再返回保存错误。
func (c *taskTerminalCoordinator) handleResultPersistenceFailure(task *model.Task, saveErr error) (handled bool, err error) {
	if errors.Is(saveErr, repository.ErrTaskStateConflict) {
		latest, latestErr := c.repo.Task(task.ID)
		if latestErr == nil && latest.Status == model.TaskStatusCancelled {
			if cancelErr := c.handleCancelledResult(*latest); cancelErr != nil {
				return true, cancelErr
			}
			return true, nil
		}
	}

	task.Status = model.TaskStatusFailed
	task.Stage = "任务结果保存失败"
	task.Error = c.userFacingMessage(saveErr)
	if terminalErr := c.markTerminalState(task); terminalErr != nil {
		return false, errors.Join(saveErr, terminalErr)
	}
	c.finalizeReplay(task, model.TaskStatusFailed, "文本回放草稿归并失败")
	_ = c.logger.log(task.UserID, task.ID, "error", "任务结果保存失败", task.Error)
	return false, saveErr
}

func (c *taskTerminalCoordinator) ensureFailedAttemptLogged(task *model.Task, err error) {
	if c.logFailedAttempt == nil || task == nil || err == nil {
		return
	}
	c.logFailedAttempt(*task, err)
}

func (c *taskTerminalCoordinator) handleSuccess(task *model.Task) error {
	c.finalizeReplay(task, model.TaskStatusSucceeded, "文本回放窗口更新失败")
	var completionErr error
	completedTask, fetchErr := c.repo.Task(task.ID)
	if fetchErr != nil {
		completionErr = fmt.Errorf("任务成功后读取任务产物失败：%w", fetchErr)
		_ = c.logger.log(task.UserID, task.ID, "error", "任务成功但读取任务产物失败", fetchErr.Error())
	} else {
		if registerErr := c.outputs.RegisterTaskOutputFromTask(*completedTask); registerErr != nil {
			// 任务成功与产物登记分开记账；登记失败保持步骤异常，允许项目页幂等补登记。
			_ = c.logger.log(task.UserID, task.ID, "error", "任务成功但项目产物登记失败", registerErr.Error())
			completionErr = fmt.Errorf("任务成功后的项目产物登记失败：%w", registerErr)
		}
	}
	_ = c.logger.log(task.UserID, task.ID, "info", "任务完成，结果已持久化", "")
	return completionErr
}

func (c *taskTerminalCoordinator) markTerminalState(task *model.Task) error {
	completedAt := time.Now()
	task.CompletedAt = &completedAt
	updated, err := c.repo.UpdateTaskTerminalState(task.ID, task.LeaseOwner, model.TaskStatusRunning, task.Status, task.Stage, task.Error, completedAt)
	if err != nil {
		return fmt.Errorf("写入任务终态失败：%w", err)
	}
	if !updated {
		return repository.ErrTaskStateConflict
	}
	// 终态是失败才退钱：成功意味着上游确实出了东西，那笔预扣就是它的价格。
	// refund 为 nil 表示当前形态不计费（桌面 / 本地装载），不是"该退但退不了"。
	if c.refund != nil && task.Status == model.TaskStatusFailed {
		c.refund.refundTaskCredits(task.UserID, task.ID, "任务失败退回预扣")
	}
	return nil
}

func (c *taskTerminalCoordinator) finalizeReplay(task *model.Task, status model.TaskStatus, message string) {
	if err := c.replay.finalizeTaskTextReplay(task.ID, status); err != nil {
		_ = c.logger.log(task.UserID, task.ID, "error", message, err.Error())
	}
}
