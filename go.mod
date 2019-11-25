module github.com/maisem/proxy-apiserver

require (
	github.com/go-logr/logr v0.1.0 // indirect
	github.com/go-logr/zapr v0.1.1 // indirect
	github.com/spf13/cobra v0.0.5
	github.com/spf13/pflag v1.0.3
	k8s.io/api v0.0.0-20190806064354-8b51d7113622
	k8s.io/apimachinery v0.0.0-20190806215851-162a2dabc72f
	k8s.io/apiserver v0.0.0-20190807221330-f03b723bf5be
	k8s.io/client-go v0.0.0-20190807061213-4fd06e107451
	k8s.io/component-base v0.0.0-20190807101431-d6d4632c35d0
	k8s.io/klog v0.3.1
	sigs.k8s.io/controller-runtime v0.1.12
)

go 1.12
