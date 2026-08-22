// Package device provides the scripted torque-device adapter. It exposes a
// Start/Read/Stop interface over a scripted device that can be programmed to
// reject, disconnect or report a stale calibration on demand, plus a
// deterministic normal path returning a receipt and a torque coefficient.
package device

import (
	"context"

	"boltforge-highstrength-joint-qa/internal/domain"
)

// Outcome enumerates the device's scripted behaviour for a single trial.
type Outcome string

const (
	// OutcomeOK returns a normal reading with a receipt.
	OutcomeOK Outcome = "OK"
	// OutcomeRejected simulates the device refusing the trial.
	OutcomeRejected Outcome = "REJECTED"
	// OutcomeDisconnected simulates a lost connection mid-trial.
	OutcomeDisconnected Outcome = "DISCONNECTED"
	// OutcomeCalibrationExpired simulates a stale calibration certificate.
	OutcomeCalibrationExpired Outcome = "CALIBRATION_EXPIRED"
)

// StartRequest binds a device to a recheck trial.
type StartRequest struct {
	DeviceID           string `json:"device_id"`
	CalibrationVersion string `json:"calibration_version"`
}

// ReadRequest asks the bound device for a coefficient reading.
type ReadRequest struct {
	DeviceID string `json:"device_id"`
}

// StopRequest releases the device at the end of a trial.
type StopRequest struct {
	DeviceID string `json:"device_id"`
}

// Reading is a single scripted coefficient reading.
type Reading struct {
	Coefficient float64 `json:"coefficient"`
	Receipt     string  `json:"receipt"`
}

// Adapter is the torque-device port. Start, Read and Stop mirror the physical
// device protocol so a scripted adapter can inject failures deterministically.
type Adapter interface {
	Start(ctx context.Context, req StartRequest) error
	Read(ctx context.Context, req ReadRequest) (Reading, error)
	Stop(ctx context.Context, req StopRequest) error
}

// MapOutcome converts a scripted failure outcome into the stable domain error
// code carried by every device rejection.
func MapOutcome(o Outcome) *domain.Error {
	switch o {
	case OutcomeRejected:
		return domain.NewError(domain.CodeDeviceRejected, "device rejected the trial")
	case OutcomeDisconnected:
		return domain.NewError(domain.CodeDeviceDisconnected, "device connection lost")
	case OutcomeCalibrationExpired:
		return domain.NewError(domain.CodeCalibrationExpired, "device calibration expired")
	default:
		return nil
	}
}
