// Package envtest starts a real kube-apiserver and etcd (the binaries
// setup-envtest downloads) without controller-runtime: it generates a CA,
// serving and client certificates, a static token file, and a kubeconfig,
// installs GoFi's CRDs, and hands back a rest.Config whose transport counts
// requests by verb so tests can assert "the second sync wrote nothing".
//
// Binaries: KUBEBUILDER_ASSETS (as setup-envtest prints it) or the first
// directory under GOFI_ENVTEST_DIR/k8s. See docs/testing.md.
package envtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"
)

// Environment is a running control plane.
type Environment struct {
	Dir        string
	Config     *rest.Config
	Kubeconfig string
	// Token authenticates as system:masters through the static token file;
	// a controller container can present it as its service account token
	// (see TokenDir).
	Token    string
	CAPEM    []byte
	Host     string
	Port     int
	Counter  *VerbCounter
	etcd     *exec.Cmd
	apiserve *exec.Cmd
	apiWait  chan error
	logs     *os.File
	stopOnce sync.Once
}

// VerbCounter counts API requests by HTTP method and resource.
type VerbCounter struct {
	mu     sync.Mutex
	counts map[string]int64
	total  atomic.Int64
}

func (c *VerbCounter) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	c.total.Add(1)
	key := req.Method + " " + resourceOf(req.URL.Path)
	c.mu.Lock()
	if c.counts == nil {
		c.counts = map[string]int64{}
	}
	c.counts[key]++
	c.mu.Unlock()
	return next.RoundTrip(req)
}

// Snapshot returns counts by "METHOD resource".
func (c *VerbCounter) Snapshot() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int64, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

// Reset zeroes the counts.
func (c *VerbCounter) Reset() {
	c.mu.Lock()
	c.counts = map[string]int64{}
	c.mu.Unlock()
	c.total.Store(0)
}

// Writes sums POST/PUT/PATCH/DELETE requests, optionally only for resource.
func (c *VerbCounter) Writes(resource string) int64 {
	var n int64
	for key, count := range c.Snapshot() {
		method, res, _ := strings.Cut(key, " ")
		if method == http.MethodGet {
			continue
		}
		if resource == "" || res == resource {
			n += count
		}
	}
	return n
}

// Total is every request since the last Reset.
func (c *VerbCounter) Total() int64 { return c.total.Load() }

// String renders sorted counts for a log line.
func (c *VerbCounter) String() string {
	snapshot := c.Snapshot()
	keys := make([]string, 0, len(snapshot))
	for k := range snapshot {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, snapshot[k]))
	}
	return strings.Join(parts, " ")
}

// resourceOf reduces an API path to its resource ("pods", "flows",
// "services"), dropping namespaces and names.
func resourceOf(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// /api/v1/namespaces/ns/pods/name  or /apis/group/version/namespaces/ns/flows/name
	for i, part := range parts {
		if part == "namespaces" && i+2 < len(parts) {
			return parts[i+2]
		}
	}
	if len(parts) >= 3 && parts[0] == "apis" {
		return parts[len(parts)-1]
	}
	if len(parts) >= 3 && parts[0] == "api" {
		return parts[2]
	}
	return strings.Join(parts, "/")
}

type counterTransport struct {
	counter *VerbCounter
	next    http.RoundTripper
}

func (t counterTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.counter.RoundTrip(req, t.next)
}

// Assets finds the directory holding kube-apiserver and etcd.
func Assets() (string, error) {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		return dir, nil
	}
	root := os.Getenv("GOFI_ENVTEST_DIR")
	if root == "" {
		return "", fmt.Errorf("set KUBEBUILDER_ASSETS (setup-envtest use -p path) or GOFI_ENVTEST_DIR")
	}
	entries, err := os.ReadDir(filepath.Join(root, "k8s"))
	if err != nil {
		return "", fmt.Errorf("GOFI_ENVTEST_DIR/k8s: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(root, "k8s", entry.Name()), nil
		}
	}
	return "", fmt.Errorf("no envtest release under %s/k8s", root)
}

// Options configure Start.
type Options struct {
	// Assets overrides Assets().
	Assets string
	// CRDDirs are directories of CRD YAML to install (charts/gofi/crds).
	CRDDirs []string
	// ExtraCRDs are CRD objects to install (for example a stand-in UDS
	// Package CRD, see PreserveUnknownCRD).
	ExtraCRDs []*unstructured.Unstructured
	// BindAll listens on 0.0.0.0 so a container on the host network or the
	// Docker bridge can reach the API server (host-gateway).
	BindAll bool
	// LogDir receives etcd/apiserver output (default: Dir).
	LogDir string
}

// Start launches etcd and kube-apiserver in a temp dir and waits for
// readiness. Stop releases everything.
func Start(ctx context.Context, opts Options) (*Environment, error) {
	assets := opts.Assets
	if assets == "" {
		var err error
		if assets, err = Assets(); err != nil {
			return nil, err
		}
	}
	for _, bin := range []string{"etcd", "kube-apiserver"} {
		if _, err := os.Stat(filepath.Join(assets, bin)); err != nil {
			return nil, fmt.Errorf("envtest asset %s: %w", bin, err)
		}
	}
	dir, err := os.MkdirTemp("", "gofi-envtest-")
	if err != nil {
		return nil, err
	}
	env := &Environment{Dir: dir, Counter: &VerbCounter{}}
	cleanup := func() { env.Stop() }

	etcdClient, etcdPeer, apiPort, err := freePorts(3)
	if err != nil {
		cleanup()
		return nil, err
	}
	logDir := opts.LogDir
	if logDir == "" {
		logDir = dir
	}
	logs, err := os.Create(filepath.Join(logDir, "control-plane.log"))
	if err != nil {
		cleanup()
		return nil, err
	}
	env.logs = logs

	etcd := exec.Command(filepath.Join(assets, "etcd"),
		"--data-dir", filepath.Join(dir, "etcd"),
		"--listen-client-urls", fmt.Sprintf("http://127.0.0.1:%d", etcdClient),
		"--advertise-client-urls", fmt.Sprintf("http://127.0.0.1:%d", etcdClient),
		"--listen-peer-urls", fmt.Sprintf("http://127.0.0.1:%d", etcdPeer),
		"--initial-advertise-peer-urls", fmt.Sprintf("http://127.0.0.1:%d", etcdPeer),
		"--initial-cluster", fmt.Sprintf("default=http://127.0.0.1:%d", etcdPeer),
		"--unsafe-no-fsync",
		"--log-level", "error",
	)
	etcd.Stdout, etcd.Stderr = logs, logs
	if err := etcd.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("etcd: %w", err)
	}
	env.etcd = etcd

	// Certificates: one CA signs the serving cert and the admin client cert.
	host := "127.0.0.1"
	if opts.BindAll {
		host = "0.0.0.0"
	}
	ca, caPEM, caKey, err := newCA()
	if err != nil {
		cleanup()
		return nil, err
	}
	env.CAPEM = caPEM
	sans := []string{"localhost", "kubernetes", "kubernetes.default", "kubernetes.default.svc", "host.docker.internal"}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("10.0.0.1")}
	if opts.BindAll {
		ips = append(ips, hostIPs()...)
	}
	servingCert, servingKey, err := newCert(ca, caKey, pkix.Name{CommonName: "kube-apiserver"}, sans, ips, false)
	if err != nil {
		cleanup()
		return nil, err
	}
	clientCert, clientKey, err := newCert(ca, caKey, pkix.Name{CommonName: "envtest-admin", Organization: []string{"system:masters"}}, nil, nil, true)
	if err != nil {
		cleanup()
		return nil, err
	}
	saKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		cleanup()
		return nil, err
	}
	saKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(saKey)})
	saPub, _ := x509.MarshalPKIXPublicKey(&saKey.PublicKey)
	saPubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: saPub})
	env.Token = randomToken()
	files := map[string][]byte{
		"ca.crt":     caPEM,
		"server.crt": servingCert,
		"server.key": servingKey,
		"client.crt": clientCert,
		"client.key": clientKey,
		"sa.key":     saKeyPEM,
		"sa.pub":     saPubPEM,
		"tokens.csv": []byte(fmt.Sprintf("%s,gofi-controller,gofi-controller-uid,\"system:masters\"\n", env.Token)),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			cleanup()
			return nil, err
		}
	}

	apiserver := exec.Command(filepath.Join(assets, "kube-apiserver"),
		"--etcd-servers", fmt.Sprintf("http://127.0.0.1:%d", etcdClient),
		"--bind-address", host,
		"--secure-port", strconv.Itoa(apiPort),
		"--endpoint-reconciler-type", "none",
		"--tls-cert-file", filepath.Join(dir, "server.crt"),
		"--tls-private-key-file", filepath.Join(dir, "server.key"),
		"--client-ca-file", filepath.Join(dir, "ca.crt"),
		"--token-auth-file", filepath.Join(dir, "tokens.csv"),
		"--service-account-issuer", "https://kubernetes.default.svc",
		"--service-account-key-file", filepath.Join(dir, "sa.pub"),
		"--service-account-signing-key-file", filepath.Join(dir, "sa.key"),
		"--service-cluster-ip-range", "10.0.0.0/24",
		"--allow-privileged=true",
		"--authorization-mode", "RBAC",
		"--disable-admission-plugins", "ServiceAccount",
		"--enable-priority-and-fairness=false",
		"--max-requests-inflight", "2000",
		"--max-mutating-requests-inflight", "1000",
		"--v", "0",
	)
	apiserver.Stdout, apiserver.Stderr = logs, logs
	if err := apiserver.Start(); err != nil {
		cleanup()
		return nil, fmt.Errorf("kube-apiserver: %w", err)
	}
	env.apiserve = apiserver
	env.Host = "127.0.0.1"
	env.Port = apiPort
	apiExited := make(chan error, 1)
	go func() { apiExited <- apiserver.Wait() }()
	env.apiWait = apiExited

	cfg := &rest.Config{
		Host:            fmt.Sprintf("https://127.0.0.1:%d", apiPort),
		TLSClientConfig: rest.TLSClientConfig{CAData: caPEM, CertData: clientCert, KeyData: clientKey},
		QPS:             1000,
		Burst:           2000,
	}
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper { return counterTransport{counter: env.Counter, next: rt} })
	env.Config = cfg

	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters["envtest"] = &clientcmdapi.Cluster{Server: cfg.Host, CertificateAuthorityData: caPEM}
	kubeconfig.AuthInfos["admin"] = &clientcmdapi.AuthInfo{ClientCertificateData: clientCert, ClientKeyData: clientKey}
	kubeconfig.Contexts["envtest"] = &clientcmdapi.Context{Cluster: "envtest", AuthInfo: "admin"}
	kubeconfig.CurrentContext = "envtest"
	env.Kubeconfig = filepath.Join(dir, "kubeconfig")
	if err := clientcmd.WriteToFile(*kubeconfig, env.Kubeconfig); err != nil {
		cleanup()
		return nil, err
	}

	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		cleanup()
		return nil, err
	}
	err = wait.PollUntilContextCancel(readyCtx, 250*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		select {
		case exitErr := <-apiExited:
			return false, fmt.Errorf("kube-apiserver exited: %v", exitErr)
		default:
		}
		_, err := cs.CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
		return err == nil, nil
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("control plane not ready (see %s): %w", logs.Name(), err)
	}
	for _, crdDir := range opts.CRDDirs {
		if err := env.InstallCRDDir(readyCtx, crdDir); err != nil {
			cleanup()
			return nil, err
		}
	}
	for _, crd := range opts.ExtraCRDs {
		if err := env.InstallCRD(readyCtx, crd); err != nil {
			cleanup()
			return nil, err
		}
	}
	env.Counter.Reset()
	return env, nil
}

// crdGVR is the CRD resource itself (installed through the dynamic client so
// the module needs no apiextensions dependency).
var crdGVR = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// InstallCRDDir applies every CustomResourceDefinition YAML in dir and waits
// until each is established.
func (e *Environment) InstallCRDDir(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		for _, doc := range strings.Split(string(data), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var object map[string]any
			if err := yaml.Unmarshal([]byte(doc), &object); err != nil {
				return fmt.Errorf("%s: %w", entry.Name(), err)
			}
			crd := &unstructured.Unstructured{Object: object}
			if crd.GetKind() != "CustomResourceDefinition" {
				continue
			}
			if err := e.InstallCRD(ctx, crd); err != nil {
				return fmt.Errorf("%s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

// InstallCRD creates (or replaces) one CRD and waits for it to be served.
func (e *Environment) InstallCRD(ctx context.Context, crd *unstructured.Unstructured) error {
	dyn, err := dynamic.NewForConfig(e.Config)
	if err != nil {
		return err
	}
	crds := dyn.Resource(crdGVR)
	crd.SetResourceVersion("")
	_, err = crds.Create(ctx, crd, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		current, getErr := crds.Get(ctx, crd.GetName(), metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		crd.SetResourceVersion(current.GetResourceVersion())
		_, err = crds.Update(ctx, crd, metav1.UpdateOptions{})
	}
	if err != nil {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		current, err := crds.Get(ctx, crd.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		conditions, _, _ := unstructured.NestedSlice(current.Object, "status", "conditions")
		for _, item := range conditions {
			cond, _ := item.(map[string]any)
			if cond["type"] == "Established" && cond["status"] == "True" {
				return true, nil
			}
		}
		return false, nil
	})
}

// PreserveUnknownCRD builds a schemaless namespaced CRD (a stand-in for a
// platform CRD such as uds.dev Packages).
func PreserveUnknownCRD(group, version, kind, plural string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": plural + "." + group},
		"spec": map[string]any{
			"group": group,
			"scope": "Namespaced",
			"names": map[string]any{"kind": kind, "plural": plural, "singular": strings.ToLower(kind)},
			"versions": []any{map[string]any{
				"name": version, "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{"type": "object", "x-kubernetes-preserve-unknown-fields": true}},
			}},
		},
	}}
}

// Clients returns typed and dynamic clients over the counting transport.
func (e *Environment) Clients() (kubernetes.Interface, dynamic.Interface, error) {
	cs, err := kubernetes.NewForConfig(e.Config)
	if err != nil {
		return nil, nil, err
	}
	dyn, err := dynamic.NewForConfig(e.Config)
	if err != nil {
		return nil, nil, err
	}
	return cs, dyn, nil
}

// EnsureNamespace creates a namespace if missing.
func (e *Environment) EnsureNamespace(ctx context.Context, name string) error {
	cs, err := kubernetes.NewForConfig(e.Config)
	if err != nil {
		return err
	}
	_, err = cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// TokenDir writes a service-account-shaped directory (token, ca.crt,
// namespace) a controller container can mount at
// /var/run/secrets/kubernetes.io/serviceaccount so rest.InClusterConfig
// reaches this control plane. It returns the directory.
func (e *Environment) TokenDir(namespace string) (string, error) {
	dir := filepath.Join(e.Dir, "serviceaccount")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for name, data := range map[string][]byte{"token": []byte(e.Token), "ca.crt": e.CAPEM, "namespace": []byte(namespace)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Stop kills both processes and removes the temp dir unless
// GOFI_ENVTEST_KEEP is set.
func (e *Environment) Stop() {
	e.stopOnce.Do(func() {
		if e.apiserve != nil && e.apiserve.Process != nil {
			_ = e.apiserve.Process.Kill()
			if e.apiWait != nil {
				<-e.apiWait
			}
		}
		if e.etcd != nil && e.etcd.Process != nil {
			_ = e.etcd.Process.Kill()
			_, _ = e.etcd.Process.Wait()
		}
		if e.logs != nil {
			e.logs.Close()
		}
		if os.Getenv("GOFI_ENVTEST_KEEP") == "" && e.Dir != "" {
			os.RemoveAll(e.Dir)
		}
	})
}

// MetricsSamples scrapes the API server's /metrics (apiserver_request_total
// and friends) through the admin certificate.
func (e *Environment) MetricsSamples(ctx context.Context) (map[string]float64, error) {
	rt, err := rest.TransportFor(e.Config)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.Config.Host+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := map[string]float64{}
	buf := make([]byte, 0, 1<<20)
	tmp := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	for _, line := range strings.Split(string(buf), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		space := strings.LastIndexByte(line, ' ')
		if space < 0 {
			continue
		}
		if v, err := strconv.ParseFloat(line[space+1:], 64); err == nil {
			out[line[:space]] = v
		}
	}
	return out, nil
}

func freePorts(n int) (int, int, int, error) {
	ports := make([]int, 0, n)
	listeners := make([]net.Listener, 0, n)
	for i := 0; i < n; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 0, 0, 0, err
		}
		listeners = append(listeners, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range listeners {
		l.Close()
	}
	return ports[0], ports[1], ports[2], nil
}

func hostIPs() []net.IP {
	var out []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			out = append(out, ipnet.IP)
		}
	}
	return out
}

func newCA() (*x509.Certificate, []byte, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "gofi-envtest-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key, nil
}

func newCert(ca *x509.Certificate, caKey *ecdsa.PrivateKey, subject pkix.Name, dns []string, ips []net.IP, client bool) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	usage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if client {
		usage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      subject,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  usage,
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// CRD is the unstructured CustomResourceDefinition type Options.ExtraCRDs takes.
type CRD = unstructured.Unstructured
