package driver

import (
	"context"
	"testing"
	"time"
)

// Listener Pods get time to drain: a preStop delay for endpoint removal, then
// the drain deadline, within the termination grace period.
func TestKubeListenerPodDrainsGracefully(t *testing.T) {
	t.Setenv(DrainTimeoutEnv, "20s")
	k, cs := newTestKube("default")
	if err := k.Create(context.Background(), testDef("drain", 9000)); err != nil {
		t.Fatal(err)
	}
	pod := flowPod(t, cs, "default", "drain")
	if grace := pod.Spec.TerminationGracePeriodSeconds; grace == nil || *grace != 30 {
		t.Fatalf("terminationGracePeriodSeconds = %v, want 30", grace)
	}
	lifecycle := pod.Spec.Containers[0].Lifecycle
	if lifecycle == nil || lifecycle.PreStop == nil || lifecycle.PreStop.Sleep == nil || lifecycle.PreStop.Sleep.Seconds != 3 {
		t.Fatalf("preStop = %+v", lifecycle)
	}
	var drain string
	for _, env := range pod.Spec.Containers[0].Env {
		if env.Name == DrainTimeoutEnv {
			drain = env.Value
		}
	}
	if drain != "20s" {
		t.Fatalf("%s = %q", DrainTimeoutEnv, drain)
	}
}

func TestDrainTimeoutConfiguration(t *testing.T) {
	t.Setenv(DrainTimeoutEnv, "")
	if DrainTimeout() != 20*time.Second {
		t.Fatal(DrainTimeout())
	}
	t.Setenv(DrainTimeoutEnv, "5s")
	if DrainTimeout() != 5*time.Second || *dockerStopTimeout() != 10 {
		t.Fatal(DrainTimeout(), *dockerStopTimeout())
	}
	t.Setenv(DrainTimeoutEnv, "soon")
	if DrainTimeout() != 20*time.Second {
		t.Fatal("invalid value not ignored")
	}
}
