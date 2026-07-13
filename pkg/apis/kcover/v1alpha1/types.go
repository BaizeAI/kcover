package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	Group   = "kcover.io"
	Version = "v1alpha1"
)

type PreflightReport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PreflightReportSpec `json:"spec"`
}

type PreflightReportSpec struct {
	WorkloadName string      `json:"workloadName"`
	WorkloadUID  string      `json:"workloadUID"`
	NodeName     string      `json:"nodeName"`
	Rank         int32       `json:"rank"`
	Report       string      `json:"report"`
	ObservedAt   metav1.Time `json:"observedAt"`
}
