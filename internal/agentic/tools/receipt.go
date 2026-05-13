package agentictools

type agenticToolReceipt struct {
	Tool            string `json:"tool"`
	Action          string `json:"action"`
	ReadOnly        bool   `json:"read_only"`
	Persistent      bool   `json:"persistent"`
	ExecutionPolicy string `json:"execution_policy"`

	ParentTaskID   int64  `json:"parent_task_id,omitempty"`
	ChildTaskID    int64  `json:"child_task_id,omitempty"`
	TaskID         int64  `json:"task_id,omitempty"`
	QueueTaskID    int64  `json:"queue_task_id,omitempty"`
	DecisionID     int64  `json:"decision_id,omitempty"`
	DecisionStatus string `json:"decision_status,omitempty"`
	Status         string `json:"status,omitempty"`
	EdgeType       string `json:"edge_type,omitempty"`
	Enqueued       bool   `json:"enqueued,omitempty"`
	Deduplicated   bool   `json:"deduplicated,omitempty"`
	Total          int    `json:"total,omitempty"`

	FromActorID int64  `json:"from_actor_id,omitempty"`
	ToActorID   int64  `json:"to_actor_id,omitempty"`
	ActorID     int64  `json:"actor_id,omitempty"`
	HandoffID   int64  `json:"handoff_id,omitempty"`
	Box         string `json:"box,omitempty"`
	Delivered   bool   `json:"delivered,omitempty"`
}
