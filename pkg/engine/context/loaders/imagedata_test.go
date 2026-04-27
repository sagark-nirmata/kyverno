package loaders

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	gcrname "github.com/google/go-containerregistry/pkg/name"
	gcrremote "github.com/google/go-containerregistry/pkg/v1/remote"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/pkg/config"
	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	enginecontext "github.com/kyverno/kyverno/pkg/engine/context"
	"github.com/kyverno/kyverno/pkg/engine/jmespath"
	"github.com/kyverno/kyverno/pkg/logging"
	tassert "github.com/stretchr/testify/assert"
	"gotest.tools/v3/assert"
)

type mockRegistryClient struct{}

func (m *mockRegistryClient) ForRef(_ context.Context, ref string) (*engineapi.ImageData, error) {
	return &engineapi.ImageData{
		Image:      ref,
		Registry:   "registry",
		Repository: "repo",
		Manifest:   []byte(`{}`),
		Config:     []byte(`{}`),
	}, nil
}

func (m *mockRegistryClient) FetchImageDescriptor(context.Context, string) (*gcrremote.Descriptor, error) {
	return nil, nil
}

func (m *mockRegistryClient) Options(context.Context) ([]gcrremote.Option, []gcrname.Option, error) {
	return nil, nil, nil
}

func (m *mockRegistryClient) NameOptions() []gcrname.Option {
	return nil
}

type mockRegistryClientFactory struct {
	client engineapi.RegistryClient
}

func (m *mockRegistryClientFactory) GetClient(context.Context, *kyvernov1.ImageRegistryCredentials, string, []string) (engineapi.RegistryClient, error) {
	return m.client, nil
}

func Test_fetchImageData_NonStringJMESPathDoesNotPanic(t *testing.T) {
	ctx := enginecontext.NewContext(jp)

	entry := kyvernov1.ContextEntry{
		Name: "test",
		ImageRegistry: &kyvernov1.ImageRegistry{
			Reference: "ghcr.io/kyverno/kyverno:latest",
			// A bare variable resolves to the raw (non-string) resolved value,
			// bypassing the usual string-producing substitution path.
			JMESPath: "{{ jmesPathVar }}",
		},
	}
	assert.NilError(t, ctx.AddContextEntry("jmesPathVar", []byte(`5`)))

	loader := NewImageDataLoader(
		context.TODO(),
		logging.GlobalLogger(),
		entry,
		ctx,
		jp,
		&mockRegistryClientFactory{client: &mockRegistryClient{}},
	)

	err := loader.LoadData()
	assert.ErrorContains(t, err, "JMESPath")
}

type mockImageDataClient struct {
	data *engineapi.ImageData
	err  error
}

func (m *mockImageDataClient) ForRef(ctx context.Context, ref string) (*engineapi.ImageData, error) {
	return m.data, m.err
}

func (m *mockImageDataClient) FetchImageDescriptor(context.Context, string) (*gcrremote.Descriptor, error) {
	return nil, nil
}

func (m *mockImageDataClient) Keychain() authn.Keychain {
	return nil
}

func (m *mockImageDataClient) Options(context.Context) ([]gcrremote.Option, []gcrname.Option, error) {
	return nil, nil, nil
}

func (m *mockImageDataClient) NameOptions() []gcrname.Option {
	return nil
}

type mockCatchErrorClientFactory struct {
	client engineapi.RegistryClient
	err    error
}

func (m *mockCatchErrorClientFactory) GetClient(ctx context.Context, creds *kyvernov1.ImageRegistryCredentials, ns string, pullSecrets []string) (engineapi.RegistryClient, error) {
	return m.client, m.err
}

func TestImageDataLoader_CatchError(t *testing.T) {
	jp := jmespath.New(config.NewDefaultConfiguration(false))
	ctx := context.Background()
	logger := logr.Discard()

	t.Run("success case", func(t *testing.T) {
		client := &mockImageDataClient{
			data: &engineapi.ImageData{
				Image:         "nginx:latest",
				ResolvedImage: "nginx@sha256:12345",
				Manifest:      []byte(`{"foo":"bar"}`),
				Config:        []byte(`{"baz":"qux"}`),
			},
		}
		factory := &mockCatchErrorClientFactory{client: client}
		engineCtx := enginecontext.NewContext(jp)
		entry := kyvernov1.ContextEntry{
			Name: "imageData",
			ImageRegistry: &kyvernov1.ImageRegistry{
				Reference:  "nginx:latest",
				CatchError: true,
			},
		}
		loader := NewImageDataLoader(ctx, logger, entry, engineCtx, jp, factory)
		err := loader.LoadData()
		tassert.NoError(t, err)

		failed, err := engineCtx.Query("imageData.failed")
		tassert.NoError(t, err)
		tassert.Equal(t, false, failed)

		errMsg, err := engineCtx.Query("imageData.errorMessage")
		tassert.NoError(t, err)
		tassert.Equal(t, "", errMsg)

		ref, err := engineCtx.Query("imageData.image")
		tassert.NoError(t, err)
		tassert.Equal(t, "nginx:latest", ref)
	})

	t.Run("failure with catchError true", func(t *testing.T) {
		client := &mockImageDataClient{
			err: errors.New("manifest not found"),
		}
		factory := &mockCatchErrorClientFactory{client: client}
		engineCtx := enginecontext.NewContext(jp)
		entry := kyvernov1.ContextEntry{
			Name: "imageData",
			ImageRegistry: &kyvernov1.ImageRegistry{
				Reference:  "non-existent:latest",
				CatchError: true,
			},
		}
		loader := NewImageDataLoader(ctx, logger, entry, engineCtx, jp, factory)
		err := loader.LoadData()
		tassert.NoError(t, err)

		failed, err := engineCtx.Query("imageData.failed")
		tassert.NoError(t, err)
		tassert.Equal(t, true, failed)

		errMsg, err := engineCtx.Query("imageData.errorMessage")
		tassert.NoError(t, err)
		tassert.Contains(t, errMsg, "manifest not found")
	})

	t.Run("failure with catchError false", func(t *testing.T) {
		client := &mockImageDataClient{
			err: errors.New("manifest not found"),
		}
		factory := &mockCatchErrorClientFactory{client: client}
		engineCtx := enginecontext.NewContext(jp)
		entry := kyvernov1.ContextEntry{
			Name: "imageData",
			ImageRegistry: &kyvernov1.ImageRegistry{
				Reference:  "non-existent:latest",
				CatchError: false,
			},
		}
		loader := NewImageDataLoader(ctx, logger, entry, engineCtx, jp, factory)
		err := loader.LoadData()
		tassert.Error(t, err)
		tassert.Contains(t, err.Error(), "manifest not found")
	})

	t.Run("GetClient failure with catchError true", func(t *testing.T) {
		factory := &mockCatchErrorClientFactory{err: errors.New("credentials error")}
		engineCtx := enginecontext.NewContext(jp)
		entry := kyvernov1.ContextEntry{
			Name: "imageData",
			ImageRegistry: &kyvernov1.ImageRegistry{
				Reference:  "nginx:latest",
				CatchError: true,
			},
		}
		loader := NewImageDataLoader(ctx, logger, entry, engineCtx, jp, factory)
		err := loader.LoadData()
		tassert.NoError(t, err)

		failed, err := engineCtx.Query("imageData.failed")
		tassert.NoError(t, err)
		tassert.Equal(t, true, failed)

		errMsg, err := engineCtx.Query("imageData.errorMessage")
		tassert.NoError(t, err)
		tassert.Contains(t, errMsg, "credentials error")
	})
}
