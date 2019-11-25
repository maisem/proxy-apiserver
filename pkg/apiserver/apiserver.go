package apiserver

import (
	"fmt"
	"strings"

	"github.com/maisem/proxy-apiserver/pkg/storage"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured/unstructuredscheme"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	genericapi "k8s.io/apiserver/pkg/endpoints"
	"k8s.io/apiserver/pkg/endpoints/discovery"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"

	"k8s.io/apiserver/pkg/apis/audit/install"
)

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()
	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs = serializer.NewCodecFactory(Scheme)
)

func init() {
	gv := schema.GroupVersion{Group: "apps.maisem.dev", Version: "v1"}
	Scheme.AddKnownTypes(gv,
		&appsv1.Deployment{},
		&appsv1.DeploymentList{},
	)
	install.Install(Scheme)

	// we need to add the options to empty v1
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
	metainternalversion.AddToScheme(Scheme)
	metainternalversion.RegisterConversions(Scheme)
	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.WatchEvent{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// ExtraConfig holds custom apiserver config
type ExtraConfig struct {
	// Place you custom config here.
	Client dynamic.Interface
}

// Config defines the config for the apiserver
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   *ExtraConfig
}

// Server contains state for a Kubernetes cluster master/api server.
type Server struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig embeds a private pointer that cannot be instantiated outside of this package.
type CompletedConfig struct {
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (cfg *Config) Complete() CompletedConfig {
	c := completedConfig{
		cfg.GenericConfig.Complete(),
		cfg.ExtraConfig,
	}

	c.GenericConfig.Version = &version.Info{
		Major: "1",
		Minor: "0",
	}

	return CompletedConfig{&c}
}

func (c completedConfig) apiGroup() *genericapiserver.APIGroupInfo {
	v1alpha1storage := map[string]rest.Storage{}
	extR := storage.GroupVersionKindResource{
		GroupVersion: schema.GroupVersion{
			Group:   "apps.maisem.dev",
			Version: "v1",
		},
		Kind:     "Deployment",
		Resource: "deployments",
	}
	intR := storage.GroupVersionKindResource{
		GroupVersion: schema.GroupVersion{
			Group:   "apps",
			Version: "v1",
		},
		Kind:     "Deployment",
		Resource: "deployments",
	}
	v1alpha1storage["deployments"] = storage.NewREST(extR, intR, true, c.ExtraConfig.Client, []string{"mdep"}, []string{"all"})
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo("apps.maisem.dev", Scheme, metav1.ParameterCodec, Codecs)
	apiGroupInfo.VersionedResourcesStorageMap["v1"] = v1alpha1storage
	apiGroupInfo.PrioritizedVersions = []schema.GroupVersion{
		{
			Group: "apps.maisem.dev", Version: "v1",
		},
	}

	return &apiGroupInfo
}

// installAPIResources is a private method for installing the REST storage backing each api groupversionresource
func installAPIResources(apiPrefix string, apiGroupInfo *genericapiserver.APIGroupInfo, s *genericapiserver.GenericAPIServer) error {
	for _, groupVersion := range apiGroupInfo.PrioritizedVersions {
		if len(apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version]) == 0 {
			klog.Warningf("Skipping API %v because it has no resources.", groupVersion)
			continue
		}

		apiGroupVersion := getAPIGroupVersion(apiGroupInfo, groupVersion, apiPrefix, s)
		if apiGroupInfo.OptionsExternalVersion != nil {
			apiGroupVersion.OptionsExternalVersion = apiGroupInfo.OptionsExternalVersion
		}

		if err := apiGroupVersion.InstallREST(s.Handler.GoRestfulContainer); err != nil {
			return fmt.Errorf("unable to setup API %v: %v", apiGroupInfo, err)
		}
	}

	return nil
}

func (c completedConfig) installAPIGroup(s *genericapiserver.GenericAPIServer) error {
	apiGroupInfo := c.apiGroup()
	if err := installAPIResources("/apis", apiGroupInfo, s); err != nil {
		return fmt.Errorf("unable to install api resources: %v", err)
	}
	// setup discovery
	// Install the version handler.
	// Add a handler at /apis/<groupName> to enumerate all versions supported by this group.
	apiVersionsForDiscovery := []metav1.GroupVersionForDiscovery{}
	for _, groupVersion := range apiGroupInfo.PrioritizedVersions {
		// Check the config to make sure that we elide versions that don't have any resources
		if len(apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version]) == 0 {
			continue
		}
		apiVersionsForDiscovery = append(apiVersionsForDiscovery, metav1.GroupVersionForDiscovery{
			GroupVersion: groupVersion.String(),
			Version:      groupVersion.Version,
		})
	}
	preferredVersionForDiscovery := metav1.GroupVersionForDiscovery{
		GroupVersion: apiGroupInfo.PrioritizedVersions[0].String(),
		Version:      apiGroupInfo.PrioritizedVersions[0].Version,
	}
	apiGroup := metav1.APIGroup{
		Name:             apiGroupInfo.PrioritizedVersions[0].Group,
		Versions:         apiVersionsForDiscovery,
		PreferredVersion: preferredVersionForDiscovery,
	}

	s.DiscoveryGroupManager.AddGroup(apiGroup)
	s.Handler.GoRestfulContainer.Add(discovery.NewAPIGroupHandler(s.Serializer, apiGroup).WebService())
	return nil
}

func getAPIGroupVersion(apiGroupInfo *genericapiserver.APIGroupInfo, groupVersion schema.GroupVersion, apiPrefix string, s *genericapiserver.GenericAPIServer) *genericapi.APIGroupVersion {
	storage := make(map[string]rest.Storage)
	for k, v := range apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version] {
		storage[strings.ToLower(k)] = v
	}
	version := newAPIGroupVersion(apiGroupInfo, groupVersion, s)
	version.Root = apiPrefix
	version.Storage = storage
	return version
}

func newAPIGroupVersion(apiGroupInfo *genericapiserver.APIGroupInfo, groupVersion schema.GroupVersion, s *genericapiserver.GenericAPIServer) *genericapi.APIGroupVersion {
	return &genericapi.APIGroupVersion{
		GroupVersion:     groupVersion,
		MetaGroupVersion: apiGroupInfo.MetaGroupVersion,

		ParameterCodec: apiGroupInfo.ParameterCodec,
		Serializer:     apiGroupInfo.NegotiatedSerializer,
		Creater:        unstructuredscheme.NewUnstructuredCreator(),
		Convertor:      Scheme,
		Defaulter:      Scheme,
		Typer:          Scheme,
		Linker:         runtime.SelfLinker(meta.NewAccessor()),

		EquivalentResourceRegistry: s.EquivalentResourceRegistry,
		Authorizer:                 s.Authorizer,
	}
}

// New returns a new instance of Server from the given config.
func (c completedConfig) New() (*Server, error) {
	s, err := c.GenericConfig.New("proxy-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}
	if err := c.installAPIGroup(s); err != nil {
		return nil, err
	}
	return &Server{s}, nil
}
