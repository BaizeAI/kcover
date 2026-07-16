//go:build metax

package node

import (
	"context"
	"fmt"
	"time"

	config "github.com/baizeai/kcover/pkg/agentconfig"
	detectorapi "github.com/baizeai/kcover/pkg/detector"
	"github.com/baizeai/kcover/pkg/events"
	"github.com/baizeai/kcover/pkg/kube"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const bufferSize = 1

const metaXGPUResourceName corev1.ResourceName = "metax-tech.com/gpu"

var _ detectorapi.Detector = (*metaXDetector)(nil)

type metaXDetector struct {
	eventCh chan events.Event
	cancel  context.CancelFunc
	doneCh  chan struct{}

	config config.MetaX
	client kubernetes.Interface

	capabilityCheck func(context.Context) (bool, error)
	checkFn         func() error
}

func newDetector(nodeName string, cfg config.Agent, client kubernetes.Interface) (detectorapi.Detector, error) {
	if client == nil {
		return nil, fmt.Errorf("kubernetes client cannot be nil for metax detector")
	}

	metaXCfg := cfg.Flavor.MetaX
	metaXCfg.NodeName = nodeName
	d := &metaXDetector{
		eventCh: make(chan events.Event, bufferSize),
		config:  metaXCfg,
		client:  client,
	}
	d.capabilityCheck = d.hasMetaXGPUCapacity
	d.checkFn = d.check

	return d, nil
}

func (d *metaXDetector) day2Check(ctx context.Context) {
	enabled, err := d.capabilityCheck(ctx)
	if err != nil {
		klog.ErrorS(err, "MetaX day2 capability check failed", "node", d.config.NodeName, "resource", metaXGPUResourceName)
		return
	}
	if !enabled {
		klog.V(2).InfoS("Skip MetaX day2 check on node without positive MetaX GPU capacity", "node", d.config.NodeName, "resource", metaXGPUResourceName)
		return
	}

	err = d.checkFn()
	if err == nil {
		return
	}
	klog.ErrorS(err, "MetaX day2 check failed")

	// TODO: Persist Day2 results in a NodeHealthReport CRD and let the
	// controller watch that durable state instead of using Events for control.
	evt := events.Event{
		ResourceType: events.Node,
		Name:         d.config.NodeName,
		Reason:       events.Day2EventReason,
		EventType:    events.Error,
		Message:      err.Error(),
	}

	select {
	case d.eventCh <- evt:
	case <-ctx.Done():
	}
}

func (d *metaXDetector) hasMetaXGPUCapacity(ctx context.Context) (bool, error) {
	if d.client == nil {
		return false, fmt.Errorf("kubernetes client is nil")
	}
	if d.config.NodeName == "" {
		return false, fmt.Errorf("node name is empty")
	}

	ctx, cancel := kube.WithRequestTimeout(ctx)
	defer cancel()

	node, err := d.client.CoreV1().Nodes().Get(ctx, d.config.NodeName, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("get node %s: %w", d.config.NodeName, err)
	}

	quantity, ok := node.Status.Capacity[metaXGPUResourceName]
	if !ok {
		return false, nil
	}

	return quantity.Sign() > 0, nil
}

func nextCheckTime(now time.Time, schedule string) (time.Time, error) {
	parsed, err := time.Parse("15:04", schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse MetaX day2 check time %q: %w", schedule, err)
	}

	hour, minute, _ := parsed.Clock()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}

	return next, nil
}

func (d *metaXDetector) Start(parent context.Context) error {
	next, err := nextCheckTime(time.Now(), d.config.Day2CheckTime)
	if err != nil {
		return err
	}

	d.doneCh = make(chan struct{})
	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel

	go func(ctx context.Context) {
		defer close(d.doneCh)
		defer close(d.eventCh)

		timer := time.NewTimer(time.Until(next))
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				d.day2Check(ctx)
				next, err := nextCheckTime(time.Now(), d.config.Day2CheckTime)
				if err != nil {
					klog.ErrorS(err, "MetaX day2 schedule became invalid", "schedule", d.config.Day2CheckTime)
					return
				}
				timer.Reset(time.Until(next))

			case <-ctx.Done():
				return
			}
		}
	}(ctx)
	return nil
}

func (d *metaXDetector) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	if d.doneCh != nil {
		<-d.doneCh
	}
}

func (d *metaXDetector) EventChan() <-chan events.Event {
	return d.eventCh
}

func (d *metaXDetector) String() string {
	return "MetaX"
}
