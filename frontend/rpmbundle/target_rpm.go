package rpmbundle

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

const (
	marinerRef = "mcr.microsoft.com/cbl-mariner/base/core:2.0"

	cachedToolkitRPMDir = "/root/.cache/mariner2-toolkit-rpm-cache"
	marinerToolkitPath  = "/usr/local/toolkit"
)

var baseMarinerPackages = []string{
	"binutils",
	"bison",
	"ca-certificates",
	"curl",
	"gawk",
	"git",
	"glibc-devel",
	"kernel-headers",
	"make",
	"msft-golang",
	"python",
	"rpm",
	"rpm-build",
	"wget",
}

var marinerTdnfCache = llb.AddMount("/var/tdnf/cache", llb.Scratch(), llb.AsPersistentCacheDir("mariner2-tdnf-cache", llb.CacheMountLocked))

var marinerBase = llb.Image(marinerRef).
	Run(
		shArgs("tdnf install -y "+strings.Join(baseMarinerPackages, " ")),
		marinerTdnfCache,
	).
	State

var toolkitImg = llb.Image("cpuguy83/mariner-toolkit:f3fee7cccffb21f1d7abf5ff940ba7db599fd4a2")

var (
	goModCache   = llb.AddMount("/go/pkg/mod", llb.Scratch(), llb.AsPersistentCacheDir("go-pkg-mod", llb.CacheMountShared))
	goBuildCache = llb.AddMount("/root/.cache/go-build", llb.Scratch(), llb.AsPersistentCacheDir("go-build-cache", llb.CacheMountShared))
)

func handleRPM(ctx context.Context, client gwclient.Client, spec *frontend.Spec) (gwclient.Reference, *image.Image, error) {
	caps := client.BuildOpts().LLBCaps
	noMerge := !caps.Contains(pb.CapMergeOp)

	st, err := specToRpmLLB(spec, noMerge, getDigestFromClientFn(ctx, client))
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err
}

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func specToRpmLLB(spec *frontend.Spec, noMerge bool, getDigest getDigestFunc) (llb.State, error) {
	br, err := specToMariner2BuildrootLLB(spec, noMerge, getDigest)
	if err != nil {
		return llb.Scratch(), err
	}

	st := marinerBase.
		Dir(marinerToolkitPath).
		Run(
			shArgs("make -j$(nproc) toolchain chroot-tools REBUILD_TOOLS=y"),
			withMarinerToolkit(),
		).
		Run(
			shArgs("make -j$(nproc) build-packages || (cat /usr/local/build/logs/pkggen/rpmbuilding/*; exit 1)"),
			withMarinerToolkit(),
			withRunMarinerPkgBuildCache(),
			llb.AddMount("/build/rpmbuild/SPECS", br, llb.SourcePath("/SPECS")),
			llb.AddEnv("SPECS_DIR", "/build/rpmbuild/SPECS"),
			llb.AddEnv("OUT_DIR", "/build/out"),
			llb.AddEnv("PROJECT_DIR", "/build/project"),
			llb.AddEnv("VERSION", spec.Version),
			llb.AddEnv("BUILD_NUMBER", spec.Revision),
			llb.AddEnv("REFRESH_WORKER_CHROOT", "n"),
			llb.Security(pb.SecurityMode_INSECURE),
			goBuildCache,
			goModCache,
		).State

	return llb.Scratch().File(
		llb.Copy(st, "/build/out", "/", frontend.WithDirContentsOnly(), frontend.WithIncludes([]string{"RPMS", "SRPMS"})),
	), nil
}

func withMarinerToolkit() llb.RunOption {
	return runOptionFunc(func(es *llb.ExecInfo) {
		llb.AddMount(marinerToolkitPath, toolkitImg, llb.AsPersistentCacheDir("mariner2-toolkit-cache", llb.CacheMountPrivate)).SetRunOption(es)

		llb.AddEnv("CHROOT_DIR", "/tmp/chroot").SetRunOption(es)
		llb.AddMount("/tmp/chroot", llb.Scratch(), llb.Tmpfs()).SetRunOption(es)

		llb.AddEnv("CACHED_RPMS_DIR", cachedToolkitRPMDir).SetRunOption(es)
		llb.AddMount(cachedToolkitRPMDir, llb.Scratch(), llb.AsPersistentCacheDir("mariner2-toolkit-rpm-cache", llb.CacheMountLocked)).SetRunOption(es)
	})
}

func withRunMarinerPkgBuildCache() llb.RunOption {
	return runOptionFunc(func(es *llb.ExecInfo) {
		llb.AddEnv("PKGBUILD_DIR", "/tmp/pkg_build_dir").SetRunOption(es)
		llb.AddMount("/tmp/pkg_build_dir", llb.Scratch(), llb.Tmpfs()).SetRunOption(es)
	})
}

type runOptionFunc func(es *llb.ExecInfo)

func (f runOptionFunc) SetRunOption(es *llb.ExecInfo) {
	f(es)
}
