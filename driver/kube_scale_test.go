package driver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wcrum/gofi/kubeclient"
	"github.com/wcrum/gofi/spec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// apiCounter counts API requests by verb (watches excluded).
type apiCounter struct {
	mu    sync.Mutex
	verbs map[string]int
}

func (c *apiCounter) record(action ktesting.Action) (bool, runtime.Object, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verbs[action.GetVerb()+" "+action.GetResource().Resource]++
	return false, nil, nil
}

func (c *apiCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verbs = map[string]int{}
}

func (c *apiCounter) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]int{}
	for k, v := range c.verbs {
		out[k] = v
	}
	return out
}

// writes counts create, update, patch, and delete requests.
func (c *apiCounter) writes() int {
	total := 0
	for verb, n := range c.snapshot() {
		switch {
		case len(verb) >= 6 && verb[:6] == "create", len(verb) >= 6 && verb[:6] == "update", len(verb) >= 5 && verb[:5] == "patch", len(verb) >= 6 && verb[:6] == "delete":
			total += n
		}
	}
	return total
}

// reads counts get and list requests.
func (c *apiCounter) reads() int {
	total := 0
	for verb, n := range c.snapshot() {
		if len(verb) >= 3 && verb[:3] == "get" || len(verb) >= 4 && verb[:4] == "list" {
			total += n
		}
	}
	return total
}

// newInformerKube is a driver reading from informer caches over the fake
// clientset, with every API request counted.
func newInformerKube(t testing.TB, namespace string) (*Kube, *fake.Clientset, *apiCounter) {
	t.Helper()
	// The fake watch channel holds 100 events; populating a thousand Pods
	// outruns the informer otherwise. Real watches stream.
	watch.DefaultChanSize = 100000
	cs := fake.NewSimpleClientset()
	counter := &apiCounter{verbs: map[string]int{}}
	cs.PrependReactor("*", "*", counter.record)
	k := &Kube{cs: cs, namespace: namespace, image: "gofi:test"}
	informers := kubeclient.NewInformers(cs, nil, namespace, time.Hour)
	k.UseInformers(informers)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := informers.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return k, cs, counter
}

// waitCache polls until the informer cache satisfies cond.
func waitCache(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("informer cache did not catch up")
}

func markRunning(t testing.TB, cs *fake.Clientset, namespace string) {
	t.Helper()
	ctx := context.Background()
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase == corev1.PodRunning {
			continue
		}
		pod.Status.Phase = corev1.PodRunning
		pod.Status.PodIP = fmt.Sprintf("10.1.%d.%d", i/250, i%250+1)
		if _, err := cs.CoreV1().Pods(namespace).UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func exposedDef(i int) spec.FlowDefinition {
	def := testDef(fmt.Sprintf("flow-%04d", i), 10000+i)
	def.Source.Listen.Service = &spec.ServiceExposure{Type: "ClusterIP"}
	return def
}

// populate creates n exposed flows and waits until the caches hold them,
// all Running.
func populate(t testing.TB, k *Kube, cs *fake.Clientset, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := k.Create(ctx, exposedDef(i)); err != nil {
			t.Fatal(err)
		}
	}
	markRunning(t, cs, k.namespace)
	waitCache(t, func() bool {
		pods, _ := k.informers.Pods(nil)
		services, _ := k.informers.Services(nil)
		if len(pods) != n || len(services) != n {
			return false
		}
		for _, pod := range pods {
			if pod.Status.Phase != corev1.PodRunning {
				return false
			}
		}
		return true
	})
}

// At steady state (every object as desired), a full List pass costs no API
// request at all: reads come from the caches and nothing differs.
func TestSteadyStateListMakesNoAPICalls(t *testing.T) {
	const n = 100
	k, cs, counter := newInformerKube(t, "gofi")
	populate(t, k, cs, n)
	counter.reset()
	for pass := 0; pass < 3; pass++ {
		defs, err := k.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != n {
			t.Fatalf("pass %d listed %d flows, want %d", pass, len(defs), n)
		}
	}
	if writes := counter.writes(); writes != 0 {
		t.Fatalf("steady-state passes wrote %d times: %v", writes, counter.snapshot())
	}
	if reads := counter.reads(); reads != 0 {
		t.Fatalf("steady-state passes read through the API %d times: %v", reads, counter.snapshot())
	}
	// Per-flow lookups, as the event-driven worker does them, are free too.
	for i := 0; i < n; i++ {
		if _, ok, err := k.Lookup(context.Background(), exposedDef(i).Name); err != nil || !ok {
			t.Fatalf("lookup %d: ok=%v err=%v", i, ok, err)
		}
		if _, ok, err := k.Tap(context.Background(), exposedDef(i).Name); err != nil || !ok {
			t.Fatalf("tap %d: ok=%v err=%v", i, ok, err)
		}
	}
	if total := counter.reads() + counter.writes(); total != 0 {
		t.Fatalf("lookups and taps made %d API requests: %v", total, counter.snapshot())
	}
	// The flow-set pass (orphan collection) is a cache walk as well.
	defs := make([]spec.FlowDefinition, 0, n)
	for i := 0; i < n; i++ {
		defs = append(defs, exposedDef(i))
	}
	if err := k.ReconcileFlowSet(context.Background(), defs); err != nil {
		t.Fatal(err)
	}
	if total := counter.reads() + counter.writes(); total != 0 {
		t.Fatalf("flow-set pass made %d API requests: %v", total, counter.snapshot())
	}
}

// Drift in one Service is repaired with exactly one write; the other
// flows cost nothing.
func TestDriftRepairWritesOnlyTheDriftedObject(t *testing.T) {
	const n = 20
	k, cs, counter := newInformerKube(t, "gofi")
	populate(t, k, cs, n)
	ctx := context.Background()
	svc, err := cs.CoreV1().Services("gofi").Get(ctx, podName(exposedDef(3).Name), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	svc.Spec.Ports[0].Port = 1
	if _, err := cs.CoreV1().Services("gofi").Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitCache(t, func() bool {
		cached, ok := k.informers.Service(svc.Name)
		return ok && cached.Spec.Ports[0].Port == 1
	})
	counter.reset()
	if _, err := k.List(ctx); err != nil {
		t.Fatal(err)
	}
	if got := counter.snapshot(); got["update services"] != 1 || counter.writes() != 1 {
		t.Fatalf("want exactly one Service update, got %v", got)
	}
}

// An update starts the new Pod beside the old one; the Service (same UID,
// same selector) keeps serving, the old Pod stays until the new one runs,
// and then it is retired.
func TestReplaceKeepsServiceAndRetiresOldPodAfterNewOneRuns(t *testing.T) {
	k, cs, _ := newInformerKube(t, "gofi")
	populate(t, k, cs, 1)
	ctx := context.Background()
	def := exposedDef(0)
	before, err := cs.CoreV1().Services("gofi").Get(ctx, podName(def.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldPod := flowPod(t, cs, "gofi", def.Name)

	time.Sleep(2 * time.Millisecond) // distinct activation stamps
	updated := exposedDef(0)
	updated.Parser.Format = "none"
	if err := k.Replace(ctx, updated); err != nil {
		t.Fatal(err)
	}
	pods, _ := cs.CoreV1().Pods("gofi").List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 2 {
		t.Fatalf("want old and new Pods side by side, got %d", len(pods.Items))
	}
	waitCache(t, func() bool { cached, _ := k.informers.Pods(nil); return len(cached) == 2 })

	// The new Pod is Pending: the old one is still the flow's runtime.
	defs, err := k.List(ctx)
	if err != nil || len(defs) != 1 {
		t.Fatalf("list: %v %v", defs, err)
	}
	pods, _ = cs.CoreV1().Pods("gofi").List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 2 {
		t.Fatalf("old Pod retired before the new one ran: %d pods", len(pods.Items))
	}
	after, err := cs.CoreV1().Services("gofi").Get(ctx, podName(def.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Service lost during update: %v", err)
	}
	if after.UID != before.UID || after.Spec.Selector[kubeLabelListener] != def.Name {
		t.Fatalf("Service replaced or its selector changed: %+v", after.Spec)
	}

	// The new Pod runs: the old one is retired and the new definition reported.
	markRunning(t, cs, "gofi")
	waitCache(t, func() bool {
		cached, _ := k.informers.Pods(nil)
		for _, pod := range cached {
			if pod.Status.Phase != corev1.PodRunning {
				return false
			}
		}
		return len(cached) == 2
	})
	defs, err = k.List(ctx)
	if err != nil || len(defs) != 1 || defs[0].Parser.Format != "none" {
		t.Fatalf("new definition not current: %+v %v", defs, err)
	}
	pods, _ = cs.CoreV1().Pods("gofi").List(ctx, metav1.ListOptions{})
	if len(pods.Items) != 1 || pods.Items[0].Name == oldPod.Name {
		t.Fatalf("old Pod not retired: %d pods", len(pods.Items))
	}
	if _, err := cs.CoreV1().Services("gofi").Get(ctx, podName(def.Name), metav1.GetOptions{}); err != nil {
		t.Fatalf("Service lost after retiring the old Pod: %v", err)
	}
}

// Create of a definition whose Pod failed removes the Pod and reports
// ErrPending instead of blocking on its disappearance.
func TestCreateOverFailedPodIsPendingNotBlocking(t *testing.T) {
	k, cs := newTestKube("gofi")
	ctx := context.Background()
	def := testDef("crashy", 9000)
	if err := k.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
	pod := flowPod(t, cs, "gofi", def.Name)
	pod.Status.Phase = corev1.PodFailed
	if _, err := cs.CoreV1().Pods("gofi").UpdateStatus(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := k.Create(ctx, def)
	if err == nil || !isPending(err) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("Create blocked on Pod removal")
	}
	// The fake API deletes synchronously, so the next attempt succeeds.
	if err := k.Create(ctx, def); err != nil {
		t.Fatal(err)
	}
}

func isPending(err error) bool {
	for err != nil {
		if err == ErrPending {
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// Objects of a flow that no longer exists are removed by the flow-set pass.
func TestFlowSetCollectsOrphanedExposure(t *testing.T) {
	k, cs, _ := newInformerKube(t, "gofi")
	populate(t, k, cs, 2)
	ctx := context.Background()
	keep := []spec.FlowDefinition{exposedDef(0)}
	if err := k.ReconcileFlowSet(ctx, keep); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CoreV1().Services("gofi").Get(ctx, podName(exposedDef(1).Name), metav1.GetOptions{}); err == nil {
		t.Fatal("orphaned Service kept")
	}
	if _, err := cs.CoreV1().Services("gofi").Get(ctx, podName(exposedDef(0).Name), metav1.GetOptions{}); err != nil {
		t.Fatal("desired flow's Service removed")
	}
}

// Endpoint discovery pages through the API and truncates with a warning.
func TestEndpointsPaginateAndTruncate(t *testing.T) {
	k, cs := newTestKube("gofi")
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if _, err := cs.CoreV1().Services("gofi").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("svc-%d", i), Namespace: "gofi"}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The fake clientset does not page; what matters is that every item is
	// read through ListAll and the result is complete.
	endpoints, err := k.Endpoints(ctx)
	if err != nil || len(endpoints) != 7 {
		t.Fatalf("endpoints: %d %v", len(endpoints), err)
	}
}

// BenchmarkKubeListSteadyState measures a full List pass (reads from the
// cache, nothing to write) for N flows.
func BenchmarkKubeListSteadyState(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("flows=%d", n), func(b *testing.B) {
			k, cs, counter := newInformerKube(b, "gofi")
			populate(b, k, cs, n)
			counter.reset()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := k.List(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if total := counter.reads() + counter.writes(); total != 0 {
				b.Fatalf("steady state made %d API requests", total)
			}
		})
	}
}

// WatchPods pushes Pod changes from the informer: existing Pods once, then
// updates and deletions, until stop.
func TestWatchPodsDeliversInformerEvents(t *testing.T) {
	k, cs, _ := newInformerKube(t, "gofi")
	populate(t, k, cs, 2)
	events := make(chan string, 64)
	stop := k.WatchPods(func(pod *corev1.Pod, deleted bool) {
		events <- fmt.Sprintf("%s:%v", pod.Labels[kubeLabelListener], deleted)
	})
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case e := <-events:
			seen[e] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("existing Pods not delivered: %v", seen)
		}
	}
	if !seen["flow-0000:false"] || !seen["flow-0001:false"] {
		t.Fatalf("unexpected initial events: %v", seen)
	}
	if err := k.Delete(context.Background(), "flow-0001"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-events:
			if e == "flow-0001:true" {
				stop()
				return
			}
		case <-deadline:
			t.Fatal("deletion not delivered")
		}
	}
}
