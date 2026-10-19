package assistant

import (
	"context"
	"fmt"

	"github.com/wcrum/gofi/kubeclient"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// SecretName holds the configuration in Kubernetes mode. A Secret rather
// than a CRD because the API key is part of it; the chart's RBAC already
// grants get/create/update on secrets (worker tokens use the same rule).
const SecretName = "gofi-assistant"

const secretKey = "config.yaml"

// KubernetesStore keeps the configuration in one Secret in GoFi's namespace.
type KubernetesStore struct {
	Client    kubernetes.Interface
	Namespace string
}

// NewKubernetesStore uses the shared in-cluster client (kubeclient).
func NewKubernetesStore(namespace string) (*KubernetesStore, error) {
	clients, err := kubeclient.Get()
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		return nil, fmt.Errorf("assistant configuration needs a namespace")
	}
	return &KubernetesStore{Client: clients.Clientset, Namespace: namespace}, nil
}

func (k *KubernetesStore) Backend() string { return "kubernetes:" + k.Namespace + "/" + SecretName }

func (k *KubernetesStore) Load(ctx context.Context) (Configuration, error) {
	s, err := k.Client.CoreV1().Secrets(k.Namespace).Get(ctx, SecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Configuration{}, nil
	}
	if err != nil {
		return Configuration{}, err
	}
	data, ok := s.Data[secretKey]
	if !ok || len(data) == 0 {
		return Configuration{}, nil
	}
	return decode(data)
}

func (k *KubernetesStore) Save(ctx context.Context, c Configuration) error {
	data, err := yaml.Marshal(resource{APIVersion: APIVersion, Kind: Kind, Spec: c})
	if err != nil {
		return err
	}
	secrets := k.Client.CoreV1().Secrets(k.Namespace)
	existing, err := secrets.Get(ctx, SecretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = secrets.Create(ctx, &core.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: SecretName, Namespace: k.Namespace, Labels: map[string]string{"app.kubernetes.io/managed-by": "gofi"}},
			Data:       map[string][]byte{secretKey: data},
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	existing.Data = map[string][]byte{secretKey: data}
	_, err = secrets.Update(ctx, existing, metav1.UpdateOptions{})
	return err
}
