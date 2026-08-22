// Package catalog is the "钢构件与连接副规则目录" domain component. It owns
// the fixed catalogue of component nodes, ordered bolt sequences, bolt/nut/
// washer batches, material grades, socket specifications, sampling rules,
// design preload / torque intervals, device calibration versions and the
// qualified-personnel directory. Every entry carries a revision so that a
// locked task snapshot can be proven immutable against later catalogue edits.
package catalog

import "boltforge-highstrength-joint-qa/internal/domain"

// BoltSpec is a single ordered bolt within a node. Bolt numbers are unique
// within a node and run as a contiguous integer sequence starting at one.
type BoltSpec struct {
	BoltNo     int    `json:"bolt_no"`
	Grade      string `json:"grade"`
	SocketSpec string `json:"socket_spec"`
}

// ComponentNode is an engineering node with an ordered bolt list and a
// revision. The GeometrySummary is frozen into the task snapshot at lock time.
type ComponentNode struct {
	Project         string          `json:"project"`
	NodeID          string          `json:"node_id"`
	GeometrySummary string          `json:"geometry_summary"`
	Revision        domain.Revision `json:"revision"`
	Bolts           []BoltSpec      `json:"bolts"`
}

// Batch is the common shape of the bolt, nut and washer batch catalogues.
type Batch struct {
	BatchNo         string          `json:"batch_no"`
	Grade           string          `json:"grade"`
	Manufacturer    string          `json:"manufacturer"`
	InspectionState string          `json:"inspection_state"`
	Revision        domain.Revision `json:"revision"`
}

// BatchKind distinguishes the three batch catalogues.
type BatchKind string

const (
	BoltBatch   BatchKind = "bolt"
	NutBatch    BatchKind = "nut"
	WasherBatch BatchKind = "washer"
)

// CompatibilityRule binds an allowed bolt/nut/washer triple and its material
// grade to a socket specification.
type CompatibilityRule struct {
	ID          string          `json:"id"`
	BoltBatch   string          `json:"bolt_batch"`
	NutBatch    string          `json:"nut_batch"`
	WasherBatch string          `json:"washer_batch"`
	Grade       string          `json:"grade"`
	SocketSpec  string          `json:"socket_spec"`
	Revision    domain.Revision `json:"revision"`
}

// TorqueDevice is a scripted torque device with a supported socket set, a
// calibration certificate version and integer torque/angle ranges.
type TorqueDevice struct {
	DeviceID           string          `json:"device_id"`
	Sockets            []string        `json:"sockets"`
	CalibrationVersion string          `json:"calibration_version"`
	TorqueRange        domain.Range    `json:"torque_range"`
	AngleRange         domain.Range    `json:"angle_range"`
	Status             string          `json:"status"`
	Revision           domain.Revision `json:"revision"`
}

// Personnel is a qualified reviewer. QualificationVersion is checked against
// the current version before a review seat is accepted.
type Personnel struct {
	PersonID             string          `json:"person_id"`
	QualificationVersion string          `json:"qualification_version"`
	Revision             domain.Revision `json:"revision"`
}

// Catalog is the read-only catalogue port consumed by the HTTP layer and by
// the task aggregate when building a locked snapshot.
type Catalog interface {
	Revision() domain.Revision
	Nodes() []ComponentNode
	Batches(kind BatchKind) []Batch
	CompatibilityRules() []CompatibilityRule
	Devices() []TorqueDevice
	Personnel() []Personnel

	NodeByID(id string) (ComponentNode, bool)
	DeviceByID(id string) (TorqueDevice, bool)
	BatchByNo(kind BatchKind, batchNo string) (Batch, bool)
	PersonnelByID(id string) (Personnel, bool)
	// MatchRule returns the compatibility rule whose grade and socket match the
	// supplied pair, used to resolve the bolt/nut/washer triple for a bolt.
	MatchRule(grade, socket string) (CompatibilityRule, bool)
	// CurrentQualificationVersion is the current required reviewer qualification
	// version, checked before a review seat is accepted.
	CurrentQualificationVersion() string
}
