package kube

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2/textlogger"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/scheme"

	testingutils "github.com/argoproj/gitops-engine/pkg/utils/testing"
	"github.com/argoproj/gitops-engine/pkg/utils/tracing"
)

func newTestResourceOperations(client *fake.FakeDynamicClient) (*kubectlResourceOperations, func()) {
	tf := cmdtesting.NewTestFactory()
	tf.FakeDynamicClient = client
	tf.UnstructuredClientForMappingFunc = func(version schema.GroupVersion) (resource.RESTClient, error) {
		return testingutils.NewFakeRESTClientBackedByDynamic(version, client), nil
	}

	ops := &kubectlResourceOperations{
		config: &rest.Config{},
		log:    textlogger.NewLogger(textlogger.NewConfig()).WithValues("application", "fake-app"),
		tracer: tracing.NopTracer{},
		fact:   tf,
	}
	return ops, tf.Cleanup
}

func TestApplyResource_Success(t *testing.T) {
	obj := testingutils.NewService()
	obj.SetNamespace("test")

	client := fake.NewSimpleDynamicClient(scheme.Scheme)
	ops, cleanup := newTestResourceOperations(client)
	defer cleanup()

	called := false
	client.PrependReactor("create", "*", func(_ k8stesting.Action) (_ bool, _ runtime.Object, _ error) {
		called = true
		return false, nil, nil
	})

	out, err := ops.ApplyResource(context.Background(), obj, cmdutil.DryRunNone, false, false, false, "test-manager")
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "service/my-service created", out)
}

func Test_kubectlResourceOperations_ApplyResource(t *testing.T) {
	newService := func() *unstructured.Unstructured {
		obj := testingutils.NewService()
		obj.SetNamespace("test")
		return obj
	}

	type args struct {
		obj             *unstructured.Unstructured
		dryRunStrategy  cmdutil.DryRunStrategy
		force           bool
		validate        bool
		serverSideApply bool
	}
	tests := []struct {
		name    string
		args    args
		client  *fake.FakeDynamicClient
		want    string
		wantErr bool
	}{
		{
			name: "success",
			args: args{
				obj:             newService(),
				dryRunStrategy:  cmdutil.DryRunNone,
				force:           false,
				validate:        false,
				serverSideApply: false,
			},
			client: fake.NewSimpleDynamicClient(scheme.Scheme),
			want:   "service/my-service created",
		},
		{
			name: "success existing",
			args: args{
				obj:             newService(),
				dryRunStrategy:  cmdutil.DryRunNone,
				force:           false,
				validate:        false,
				serverSideApply: false,
			},
			client: fake.NewSimpleDynamicClient(scheme.Scheme, newService()),
			want:   "service/my-service created",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops, cleanup := newTestResourceOperations(tt.client)
			defer cleanup()

			got, err := ops.ApplyResource(context.Background(), tt.args.obj, tt.args.dryRunStrategy, tt.args.force, tt.args.validate, tt.args.serverSideApply, "test-manager")
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorContains(t, err, tt.want)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// func TestApplyResource_RunResourceCommandError(t *testing.T) {
// 	obj := &unstructured.Unstructured{}
// 	obj.SetKind("ConfigMap")
// 	obj.SetName("fail-cm")
// 	obj.SetNamespace("default")

// 	mockOps := &mockKubectlResourceOperations{
// 		kubectlResourceOperations: kubectlResourceOperations{
// 			config: &fakeRestConfig{Host: "https://k8s.example.com"},
// 			log:    testr.New(t),
// 			tracer: &mockTracer{},
// 		},
// 		runResourceCommandFunc: func(ctx context.Context, o *unstructured.Unstructured, dryRunStrategy cmdutil.DryRunStrategy, executor commandExecutor) (string, error) {
// 			return "", errors.New("runResourceCommand failed")
// 		},
// 	}

// 	out, err := mockOps.ApplyResource(context.Background(), obj, cmdutil.DryRunNone, false, false, false, "test-manager")
// 	assert.Error(t, err)
// 	assert.Contains(t, err.Error(), "runResourceCommand failed")
// 	assert.Empty(t, out)
// }

// func TestApplyResource_LogsWithDryRun(t *testing.T) {
// 	obj := &unstructured.Unstructured{}
// 	obj.SetKind("ConfigMap")
// 	obj.SetName("dryrun-cm")
// 	obj.SetNamespace("default")

// 	mockOps := &mockKubectlResourceOperations{
// 		kubectlResourceOperations: kubectlResourceOperations{
// 			config: &fakeRestConfig{Host: "https://k8s.example.com"},
// 			log:    textlogger.NewLogger(textlogger.NewConfig()).WithValues("application", "fake-app"),
// 			tracer: &mockTracer{},
// 		},
// 		runResourceCommandFunc: func(ctx context.Context, o *unstructured.Unstructured, dryRunStrategy cmdutil.DryRunStrategy, executor commandExecutor) (string, error) {
// 			return "dryrun applied", nil
// 		},
// 	}

// 	out, err := mockOps.ApplyResource(context.Background(), obj, cmdutil.DryRunClient, false, false, false, "test-manager")
// 	assert.NoError(t, err)
// 	assert.Equal(t, "dryrun applied", out)
// }
