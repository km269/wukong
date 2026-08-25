// Package artifact provides artifact service backend factory for
// selecting the appropriate storage backend based on configuration.
package artifact

import (
	"fmt"
	"os"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactcos "trpc.group/trpc-go/trpc-agent-go/artifact/cos"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
)

// NewService creates an artifact service based on configuration.
// Supported backends:
//   - "inmemory" (default): In-memory storage for development.
//   - "cos": Tencent Cloud Object Storage for production.
func NewService(cfg *config.ArtifactConfig) (artifact.Service, error) {
	switch cfg.Backend {
	case "cos":
		return newCOSService(cfg)
	case "inmemory", "":
		return newInMemoryService(), nil
	default:
		return nil, fmt.Errorf(
			"unsupported artifact backend: %s", cfg.Backend,
		)
	}
}

// newInMemoryService creates an in-memory artifact service.
func newInMemoryService() artifact.Service {
	return artifactinmemory.NewService()
}

// newCOSService creates a Tencent Cloud COS-backed artifact service.
// Credentials are resolved in order:
//  1. Config file (cos_secret_id / cos_secret_key)
//  2. Environment variables (COS_SECRETID / COS_SECRETKEY)
func newCOSService(cfg *config.ArtifactConfig) (artifact.Service, error) {
	bucketURL := cfg.COSBucketURL
	if bucketURL == "" {
		return nil, fmt.Errorf(
			"cos_bucket_url is required for cos artifact backend")
	}

	secretID := cfg.COSSecretID
	if secretID == "" {
		secretID = os.Getenv("COS_SECRETID")
	}
	secretKey := cfg.COSSecretKey
	if secretKey == "" {
		secretKey = os.Getenv("COS_SECRETKEY")
	}

	if secretID == "" || secretKey == "" {
		return nil, fmt.Errorf(
			"cos credentials required: set cos_secret_id/cos_secret_key " +
				"or COS_SECRETID/COS_SECRETKEY environment variables")
	}

	return artifactcos.NewService(
		"wukong-artifact",
		bucketURL,
		artifactcos.WithSecretID(secretID),
		artifactcos.WithSecretKey(secretKey),
	)
}
