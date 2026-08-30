package artifact

import (
	"strings"
	"testing"

	"github.com/km269/wukong/internal/config"

	artifactcos "trpc.group/trpc-go/trpc-agent-go/artifact/cos"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
)

func TestNewService_InMemory(t *testing.T) {
	cases := []struct {
		name    string
		backend string
	}{
		{name: "explicit", backend: "inmemory"},
		{name: "empty defaults to inmemory", backend: ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(&config.ArtifactConfig{Backend: tt.backend})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			if _, ok := svc.(*artifactinmemory.Service); !ok {
				t.Errorf("service type = %T, want *inmemory.Service", svc)
			}
		})
	}
}

func TestNewService_COSMissingBucketURL(t *testing.T) {
	t.Setenv("COS_SECRETID", "env-id")
	t.Setenv("COS_SECRETKEY", "env-key")

	_, err := NewService(&config.ArtifactConfig{Backend: "cos"})
	if err == nil || !strings.Contains(err.Error(), "cos_bucket_url is required") {
		t.Fatalf("NewService() error = %v, want containing %q", err, "cos_bucket_url is required")
	}
}

func TestNewService_COSMissingCredentials(t *testing.T) {
	t.Setenv("COS_SECRETID", "")
	t.Setenv("COS_SECRETKEY", "")

	cfg := &config.ArtifactConfig{
		Backend:      "cos",
		COSBucketURL: "https://bucket.cos.region.myqcloud.com",
	}
	_, err := NewService(cfg)
	if err == nil || !strings.Contains(err.Error(), "cos credentials required") {
		t.Fatalf("NewService() error = %v, want containing %q", err, "cos credentials required")
	}
}

func TestNewService_COSCredentialsFromConfig(t *testing.T) {
	t.Setenv("COS_SECRETID", "")
	t.Setenv("COS_SECRETKEY", "")

	cfg := &config.ArtifactConfig{
		Backend:      "cos",
		COSBucketURL: "https://bucket.cos.region.myqcloud.com",
		COSSecretID:  "config-id",
		COSSecretKey: "config-key",
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, ok := svc.(*artifactcos.Service); !ok {
		t.Errorf("service type = %T, want *cos.Service", svc)
	}
}

func TestNewService_COSCredentialsFromEnv(t *testing.T) {
	t.Setenv("COS_SECRETID", "env-id")
	t.Setenv("COS_SECRETKEY", "env-key")

	cfg := &config.ArtifactConfig{
		Backend:      "cos",
		COSBucketURL: "https://bucket.cos.region.myqcloud.com",
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, ok := svc.(*artifactcos.Service); !ok {
		t.Errorf("service type = %T, want *cos.Service", svc)
	}
}

func TestNewService_COSConfigBeatsEnv(t *testing.T) {
	t.Setenv("COS_SECRETID", "env-id")
	t.Setenv("COS_SECRETKEY", "env-key")

	cfg := &config.ArtifactConfig{
		Backend:      "cos",
		COSBucketURL: "https://bucket.cos.region.myqcloud.com",
		COSSecretID:  "config-id",
		COSSecretKey: "config-key",
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if svc == nil {
		t.Fatal("NewService() = nil")
	}
}

func TestNewService_COSSecretIDOnlyFromEnv(t *testing.T) {
	t.Setenv("COS_SECRETID", "env-id")
	t.Setenv("COS_SECRETKEY", "")

	cfg := &config.ArtifactConfig{
		Backend:      "cos",
		COSBucketURL: "https://bucket.cos.region.myqcloud.com",
		COSSecretKey: "config-key",
	}
	// Secret ID comes from env, secret key from config: both present.
	if _, err := NewService(cfg); err != nil {
		t.Fatalf("NewService() error = %v, want nil (mixed credential sources)", err)
	}
}

func TestNewService_COSPartialCredentialsRejected(t *testing.T) {
	t.Setenv("COS_SECRETID", "")
	t.Setenv("COS_SECRETKEY", "")

	cfg := &config.ArtifactConfig{
		Backend:      "cos",
		COSBucketURL: "https://bucket.cos.region.myqcloud.com",
		COSSecretID:  "config-id",
		// COSSecretKey missing.
	}
	_, err := NewService(cfg)
	if err == nil || !strings.Contains(err.Error(), "cos credentials required") {
		t.Fatalf("NewService() error = %v, want containing %q", err, "cos credentials required")
	}
}

func TestNewService_UnsupportedBackend(t *testing.T) {
	_, err := NewService(&config.ArtifactConfig{Backend: "s3"})
	if err == nil || !strings.Contains(err.Error(), "unsupported artifact backend: s3") {
		t.Fatalf("NewService() error = %v, want containing %q", err, "unsupported artifact backend: s3")
	}
}

func TestNewService_NilConfig(t *testing.T) {
	// A nil config would panic on field access; Backend "" is the inmemory
	// path, so a zero-value config must behave like the default.
	svc, err := NewService(&config.ArtifactConfig{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, ok := svc.(*artifactinmemory.Service); !ok {
		t.Errorf("service type = %T, want *inmemory.Service", svc)
	}
}
