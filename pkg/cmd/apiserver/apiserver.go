package apiserver

import (
	"io"
	"net"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/dynamic"
	clientconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	//"k8s.io/sample-apiserver/pkg/apis/wardle/v1alpha1"
	"github.com/maisem/proxy-apiserver/pkg/apiserver"
	//	informers "k8s.io/sample-apiserver/pkg/generated/informers/externalversions"
)

// ServerOptions contains state for master/api server
type ServerOptions struct {
	SecureServing  *genericoptions.SecureServingOptionsWithLoopback
	Authentication *genericoptions.DelegatingAuthenticationOptions
	Authorization  *genericoptions.DelegatingAuthorizationOptions
	// Audit          *genericoptions.AuditOptions
	// CoreAPI        *genericoptions.CoreAPIOptions

	// ProcessInfo is used to identify events created by the server.
	ProcessInfo *genericoptions.ProcessInfo

	StdOut io.Writer
	StdErr io.Writer
}

// NewServerOptions returns a new ServerOptions
func NewServerOptions(out, errOut io.Writer) *ServerOptions {
	o := &ServerOptions{
		SecureServing:  genericoptions.NewSecureServingOptions().WithLoopback(),
		ProcessInfo:    genericoptions.NewProcessInfo("proxy-apiserver", "proxy"),
		Authentication: genericoptions.NewDelegatingAuthenticationOptions(),
		Authorization:  genericoptions.NewDelegatingAuthorizationOptions(),
		StdOut:         out,
		StdErr:         errOut,
	}
	return o
}

// NewCommandStartServer provides a CLI handler for 'start master' command
// with a default ServerOptions.
func NewCommandStartServer(defaults *ServerOptions, stopCh <-chan struct{}) *cobra.Command {
	o := *defaults
	cmd := &cobra.Command{
		Short: "Launch a proxy API server",
		Long:  "Launch a proxy API server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.Run(stopCh); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	o.AddFlags(flags)
	utilfeature.DefaultMutableFeatureGate.AddFlag(flags)

	return cmd
}

func (o ServerOptions) AddFlags(fs *pflag.FlagSet) {
	o.SecureServing.AddFlags(fs)
	o.Authentication.AddFlags(fs)
	o.Authorization.AddFlags(fs)
}

func (o ServerOptions) Complete() error {
	return o.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")})
}

// Validate validates ServerOptions
func (o ServerOptions) Validate(args []string) error {
	errs := append([]error{}, o.SecureServing.Validate()...)
	errs = append(errs, o.Authentication.Validate()...)
	errs = append(errs, o.Authorization.Validate()...)
	return utilerrors.NewAggregate(errs)
}

func (o *ServerOptions) ApplyTo(cfg *genericapiserver.RecommendedConfig) error {
	if err := o.SecureServing.ApplyTo(&cfg.SecureServing, &cfg.LoopbackClientConfig); err != nil {
		return err
	}
	if err := o.Authentication.ApplyTo(&cfg.Authentication, cfg.SecureServing, cfg.OpenAPIConfig); err != nil {
		return err
	}
	if err := o.Authorization.ApplyTo(&cfg.Authorization); err != nil {
		return err
	}
	return nil
}

// Config returns config for the api server given ServerOptions
func (o *ServerOptions) Config() (*apiserver.Config, error) {
	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)
	if err := o.ApplyTo(serverConfig); err != nil {
		return nil, err
	}
	config := &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig: &apiserver.ExtraConfig{
			Client: dynamic.NewForConfigOrDie(clientconfig.GetConfigOrDie()),
		},
	}
	return config, nil
}

// RunServer starts a new Server given ServerOptions
func (o ServerOptions) Run(stopCh <-chan struct{}) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	server, err := config.Complete().New()
	if err != nil {
		return err
	}
	return server.GenericAPIServer.PrepareRun().Run(stopCh)
}
