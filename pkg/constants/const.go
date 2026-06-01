package constants

const (
	KubeflowJobLabel                  = "training.kubeflow.org/job-name"
	LeaderWorkerSetNameLabel          = "leaderworkerset.sigs.k8s.io/name"
	LeaderWorkerSetGroupIndexLabel    = "leaderworkerset.sigs.k8s.io/group-index"
	LeaderWorkerSetWorkerIndexLabel   = "leaderworkerset.sigs.k8s.io/worker-index"
	BatchJobNameLabel                 = "batch.kubernetes.io/job-name"
	BatchJobCompletionIndexAnnotation = "batch.kubernetes.io/job-completion-index"
	PreflightLabel                    = "kcover.io/preflight"
	// PreflightNamespaceAnnotation carries the workload namespace associated with
	// a preflight report event.
	PreflightNamespaceAnnotation = "kcover.io/preflight-namespace"
	PreflightPayloadAnnotation   = "kcover.io/preflight-payload"
	PreflightDedupKeyAnnotation  = "kcover.io/preflight-dedup-key"
	// PreflightWorkloadAnnotation carries the training or inference workload name
	// associated with a preflight report event.
	PreflightWorkloadAnnotation = "kcover.io/preflight-workload"
	NodeNameEnv                 = "NODE_NAME"
	LegacyNodeNameEnv           = "FAST_RECOVERY_NODE_NAME"
	// recovery annotations
	NeedRecoveryAnnotation = "kcover.io/need-recovery"

	EnabledRecoveryLabel = "kcover.io/cascading-recovery"

	True = "true"
)
