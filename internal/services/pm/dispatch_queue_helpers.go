package pm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

func isPMDispatchTerminalStatus(st contracts.PMDispatchJobStatus) bool {
	switch st {
	case contracts.PMDispatchSucceeded, contracts.PMDispatchFailed:
		return true
	default:
		return false
	}
}

func newPMDispatchRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("dsp_%d", time.Now().UnixNano())
	}
	return "dsp_" + hex.EncodeToString(buf)
}

func newPMDispatchRunnerID() string {
	return fmt.Sprintf("runner-%d-%s", os.Getpid(), strings.TrimPrefix(newPMDispatchRequestID(), "dsp_"))
}

func isPMDispatchRequestIDUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "pm_dispatch_jobs.request_id")
}

func (s *Service) getPMDispatchJob(ctx context.Context, jobID uint) (contracts.PMDispatchJob, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.PMDispatchJob{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return contracts.PMDispatchJob{}, fmt.Errorf("job_id 不能为空")
	}
	var job contracts.PMDispatchJob
	if err := db.WithContext(ctx).First(&job, jobID).Error; err != nil {
		return contracts.PMDispatchJob{}, err
	}
	return job, nil
}

func (s *Service) waitPMDispatchJob(ctx context.Context, jobID uint, pollInterval time.Duration) (contracts.PMDispatchJob, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if pollInterval <= 0 {
		pollInterval = defaultDispatchPollInterval
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		job, err := s.getPMDispatchJob(ctx, jobID)
		if err != nil {
			return contracts.PMDispatchJob{}, err
		}
		if isPMDispatchTerminalStatus(job.Status) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return contracts.PMDispatchJob{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
