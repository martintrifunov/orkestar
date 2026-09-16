package daemon

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// maxLeaseDurationMS is the largest millisecond value that still fits in a
// time.Duration (int64 nanoseconds) without overflowing. A larger value would
// wrap, and a negative one is rejected outright: either could turn a bounded
// exclusive lease into a permanent one.
const maxLeaseDurationMS = int64(math.MaxInt64) / int64(time.Millisecond)

func (s *Server) acquireLease(rawParams json.RawMessage) (workflow.Lease, error) {
	var params struct {
		Resource   string `json:"resource"`
		HolderID   string `json:"holder_id"`
		Mode       string `json:"mode"`
		DurationMS int64  `json:"duration_ms"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Lease{}, fmt.Errorf("decode lease acquire params: %w", err)
	}
	if params.DurationMS < 0 {
		return workflow.Lease{}, fmt.Errorf("lease duration_ms must not be negative")
	}
	if params.DurationMS > maxLeaseDurationMS {
		return workflow.Lease{}, fmt.Errorf("lease duration_ms is too large")
	}
	duration := time.Duration(params.DurationMS) * time.Millisecond
	return s.leases.Acquire(params.Resource, params.HolderID, workflow.LeaseMode(params.Mode), duration)
}

func (s *Server) releaseLease(rawParams json.RawMessage) (map[string]string, error) {
	var params struct {
		Resource string `json:"resource"`
		LeaseID  string `json:"lease_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode lease release params: %w", err)
	}
	if err := s.leases.Release(params.Resource, params.LeaseID); err != nil {
		return nil, err
	}
	return map[string]string{"status": "released"}, nil
}
