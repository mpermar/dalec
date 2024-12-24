package photon

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	AzLinux3TargetKey     = "azlinux3"
	tdnfCacheNameAzlinux3 = "azlinux3-tdnf-cache"

	// Photon5Ref is the image ref used for the base worker image
	Photon5Ref      = "docker.io/photon:5"
	Photon5FullName = "Photon 5"
	// Photon5WorkerContextName is the build context name that can be used to lookup
	Photon5WorkerContextName = "dalec-photon5-worker"
	//azlinux3DistrolessRef     = "mcr.microsoft.com/azurelinux/distroless/base:3.0"
)

func NewPhoton5Handler() gwclient.BuildFunc {
	return newHandler(photon5{})
}

type photon5 struct{}

func (w photon5) Base(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	worker, err := sOpt.GetContext(Photon5WorkerContextName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch(), err
	}
	if worker != nil {
		return *worker, nil
	}

	st := frontend.GetBaseImage(sOpt, Photon5Ref)
	return st.Run(
		w.Install([]string{"rpm-build", "mariner-rpm-macros", "build-essential", "ca-certificates"}, installWithConstraints(opts)),
		dalec.WithConstraints(opts...),
	).Root(), nil
}

func (w photon5) Install(pkgs []string, opts ...installOpt) llb.RunOption {
	var cfg installConfig
	setInstallOptions(&cfg, opts)
	return dalec.WithRunOptions(tdnfInstall(&cfg, "3.0", pkgs), w.tdnfCacheMount(cfg.root))
}

func (w photon5) BasePackages() []string {
	return []string{"ca-certificates"}
}

func (photon5) DefaultImageConfig(ctx context.Context, resolver llb.ImageMetaResolver, platform *ocispecs.Platform) (*dalec.DockerImageSpec, error) {
	_, _, dt, err := resolver.ResolveImageConfig(ctx, Photon5Ref, sourceresolver.Opt{Platform: platform})
	if err != nil {
		return nil, err
	}

	var cfg dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (photon5) tdnfCacheMount(root string) llb.RunOption {
	return llb.AddMount(filepath.Join(root, tdnfCacheDir), llb.Scratch(), llb.AsPersistentCacheDir(tdnfCacheNameAzlinux3, llb.CacheMountLocked))
}

func (photon5) FullName() string {
	return Photon5FullName
}
