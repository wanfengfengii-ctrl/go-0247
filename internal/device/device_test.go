package device_test

import (
	"context"
	"testing"

	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
)

func TestScriptedNormalReadingsDeterministic(t *testing.T) {
	d := device.NewScripted(nil)
	if err := d.Start(context.Background(), device.StartRequest{DeviceID: "TD-001", CalibrationVersion: "CAL-1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	r1, err := d.Read(context.Background(), device.ReadRequest{DeviceID: "TD-001"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	r2, err := d.Read(context.Background(), device.ReadRequest{DeviceID: "TD-001"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if r1.Receipt == r2.Receipt {
		t.Fatalf("receipts should differ: %q", r1.Receipt)
	}
	if r1.Coefficient <= 0 || r1.Coefficient > 0.2 {
		t.Fatalf("coefficient out of expected range: %v", r1.Coefficient)
	}
}

func TestScriptedFailureOutcomes(t *testing.T) {
	cases := []struct {
		program []device.Outcome
		code    domain.Code
	}{
		{[]device.Outcome{device.OutcomeRejected}, domain.CodeDeviceRejected},
		{[]device.Outcome{device.OutcomeDisconnected}, domain.CodeDeviceDisconnected},
		{[]device.Outcome{device.OutcomeCalibrationExpired}, domain.CodeCalibrationExpired},
	}
	for _, c := range cases {
		d := device.NewScripted(c.program)
		err := d.Start(context.Background(), device.StartRequest{DeviceID: "TD-001"})
		if !domain.IsCode(err, c.code) {
			t.Errorf("program %v: want %s, got %v", c.program, c.code, err)
		}
	}
}
