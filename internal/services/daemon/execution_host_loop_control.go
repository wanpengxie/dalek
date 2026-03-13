package daemon

import (
	"strings"
	"time"
)

type executionTicketLoopControlSink struct {
	host   *ExecutionHost
	handle *executionRunHandle
}

func (s executionTicketLoopControlSink) LoopClaimed(ticketID, workerID uint) {
	if s.host == nil || s.handle == nil {
		return
	}
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	if ticketID != 0 {
		s.handle.ticketID = ticketID
	}
	if workerID != 0 {
		s.handle.workerID = workerID
	}
	if strings.TrimSpace(s.handle.phase) == "" {
		s.handle.phase = ticketLoopPhaseClaimed
	}
}

func (s executionTicketLoopControlSink) LoopRunAttached(runID, workerID uint, phase string) {
	if s.host == nil || s.handle == nil {
		return
	}
	if runID != 0 {
		s.host.attachHandleRun(s.handle, runID, workerID)
	} else if workerID != 0 {
		s.host.mu.Lock()
		s.handle.workerID = workerID
		s.host.mu.Unlock()
	}
	s.host.setHandlePhase(s.handle, phase)
}

func (s executionTicketLoopControlSink) LoopClosing() {
	if s.host == nil || s.handle == nil {
		return
	}
	s.host.setHandlePhase(s.handle, ticketLoopPhaseClosing)
}

func (s executionTicketLoopControlSink) LoopCancelRequested() {
	if s.host == nil || s.handle == nil {
		return
	}
	now := time.Now()
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	s.handle.phase = ticketLoopPhaseCancel
	s.handle.cancelRequestedAt = &now
}

func (s executionTicketLoopControlSink) LoopErrored(err error) {
	if s.host == nil || s.handle == nil {
		return
	}
	s.host.mu.Lock()
	defer s.host.mu.Unlock()
	s.handle.phase = ticketLoopPhaseErrored
	if err != nil {
		s.handle.lastError = strings.TrimSpace(err.Error())
	}
}

func (h *ExecutionHost) setHandlePhase(handle *executionRunHandle, phase string) {
	if h == nil || handle == nil {
		return
	}
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return
	}
	h.mu.Lock()
	handle.phase = phase
	h.mu.Unlock()
}
