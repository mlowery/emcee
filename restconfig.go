package emcee

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type RestConfigGetter interface {
	Get() ([]*NamedRestConfig, error)
}

func NewKubeContextGetter(kubeconfig string, contexts []string) *kubeContextGetter {
	return &kubeContextGetter{
		kubeconfig: kubeconfig,
		contexts:   contexts,
	}
}

var _ RestConfigGetter = &kubeContextGetter{}

type kubeContextGetter struct {
	kubeconfig string
	contexts   []string
}

func (g *kubeContextGetter) Get() ([]*NamedRestConfig, error) {
	var rc []*NamedRestConfig
	for _, context := range g.contexts {
		restConfig, err := getClientConfig(g.kubeconfig, context).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get rest config for context %q: %w", context, err)
		}
		rc = append(rc, &NamedRestConfig{
			ConfigName: context,
			Config:     *restConfig,
		})
	}
	return rc, nil
}

func getClientConfig(kubeconfig, context string) clientcmd.ClientConfig {
	pathOptions := clientcmd.NewDefaultPathOptions()
	loadingRules := *pathOptions.LoadingRules
	loadingRules.Precedence = pathOptions.GetLoadingPrecedence()
	loadingRules.ExplicitPath = kubeconfig
	overrides := &clientcmd.ConfigOverrides{
		CurrentContext: context,
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&loadingRules, overrides)
}

var _ RestConfigGetter = &crGetter{}

type crGetter struct {
	kubeconfig          string
	context             string
	selector            string
	namespace           string
	clusterToConfigName clusterToConfigNameFunc
}

type clusterToConfigNameFunc func(*crCluster) string

func ClusterNameFunc(cl *crCluster) string {
	return cl.Name
}
func MakeClusterLabelFunc(label string) clusterToConfigNameFunc {
	return func(cl *crCluster) string {
		return cl.Labels[label]
	}
}

type crCluster struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              struct {
		KubernetesAPIEndpoints struct {
			ServerEndpoints []struct {
				ServerAddress string `json:"serverAddress,omitempty"`
			} `json:"serverEndpoints,omitempty"`
		} `json:"kubernetesApiEndpoints,omitempty"`
	} `json:"spec,omitempty"`
}

func unstructToCluster(unstruct unstructured.Unstructured) (*crCluster, error) {
	clusterBytes, err := unstruct.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal unstructured JSON: %w", err)
	}
	var c crCluster
	err = json.Unmarshal(clusterBytes, &c)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal unstructured JSON: %w", err)
	}
	return &c, nil
}

func NewCRGetter(kubeconfig string, context string, selector string, namespace string, clusterToConfigName clusterToConfigNameFunc) *crGetter {
	return &crGetter{
		kubeconfig:          kubeconfig,
		context:             context,
		selector:            selector,
		namespace:           namespace,
		clusterToConfigName: clusterToConfigName,
	}
}

func (g *crGetter) Get() ([]*NamedRestConfig, error) {
	// using dynamic client because https://github.com/kubernetes/cluster-registry is ancient
	restConfig, err := getClientConfig(g.kubeconfig, g.context).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get rest config for kube client: %w", err)
	}
	dClientGen, err := newClientGen(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client generator: %w", err)
	}
	// cluster objects live in namespace "default"
	dClient, err := dClientGen.Gen(schema.GroupVersionKind{
		Group: "clusterregistry.k8s.io",
		Kind:  "Cluster",
	}, g.namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	clusterList, err := dClient.List(ctx, metav1.ListOptions{
		LabelSelector: g.selector,
	})
	var rc []*NamedRestConfig
	for _, cluster := range clusterList.Items {
		cl, err := unstructToCluster(cluster)
		if err != nil {
			return nil, fmt.Errorf("failed to convert unstructured to structured: %w", err)
		}
		rc = append(rc, g.makeRestConfig(restConfig, cl))
	}
	return rc, nil
}

func (g *crGetter) makeRestConfig(restConfig *rest.Config, cl *crCluster) *NamedRestConfig {
	// reuse the cr-context but replace the host from crCluster
	rcCopy := rest.CopyConfig(restConfig)
	rcCopy.Host = cl.Spec.KubernetesAPIEndpoints.ServerEndpoints[0].ServerAddress
	// TODO(mlowery): Fix this.
	rcCopy.TLSClientConfig.Insecure = true
	rcCopy.TLSClientConfig.CAData = nil
	rcCopy.TLSClientConfig.CAFile = ""
	if !strings.HasPrefix(rcCopy.Host, "https://") {
		rcCopy.Host = "https://" + rcCopy.Host
	}
	return &NamedRestConfig{
		ConfigName: g.clusterToConfigName(cl),
		Config:     *rcCopy,
	}
}

type clientGen struct {
	restMapper meta.RESTMapper
	client     dynamic.Interface
}

func newClientGen(config *rest.Config) (*clientGen, error) {
	config.Timeout = 5 * time.Minute
	restMapper, err := newDiscoveryRESTMapper(config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get discovery rest mapper")
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get create dynamic client")
	}
	return &clientGen{
		restMapper: restMapper,
		client:     dynamicClient,
	}, nil
}

func newDiscoveryRESTMapper(c *rest.Config) (meta.RESTMapper, error) {
	// Get a mapper
	dc, err := discovery.NewDiscoveryClientForConfig(c)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create discovery client")
	}
	gr, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get api group resources")
	}
	return restmapper.NewDiscoveryRESTMapper(gr), nil
}

func (c *clientGen) Gen(gvk schema.GroupVersionKind, ns string) (dynamic.ResourceInterface, error) {
	mapping, err := c.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get rest mapping")
	}
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		return c.client.Resource(mapping.Resource), nil
	}
	return c.client.Resource(mapping.Resource).Namespace(ns), nil
}
