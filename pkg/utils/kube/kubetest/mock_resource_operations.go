package kubetest

import (
	"context"
	"sync"

	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
)

type RegisteredCommand struct {
	Command                string
	Validate               bool
	ServerSideApply        bool
	ServerSideApplyManager string
	Force                  bool
	DryRunStrategy         cmdutil.DryRunStrategy
}

type MockResourceOps struct {
	Commands      map[string]KubectlOutput
	Events        chan watch.Event
	DynamicClient dynamic.Interface

	commandsPerResource map[kube.ResourceKey][]RegisteredCommand

	recordLock sync.RWMutex

	getResourceFunc *func(ctx context.Context, config *rest.Config, gvk schema.GroupVersionKind, name string, namespace string) (*unstructured.Unstructured, error)
}

// WithGetResourceFunc overrides the default ConvertToVersion behavior.
func (r *MockResourceOps) WithGetResourceFunc(getResourcefunc func(context.Context, *rest.Config, schema.GroupVersionKind, string, string) (*unstructured.Unstructured, error)) *MockResourceOps {
	r.getResourceFunc = &getResourcefunc
	return r
}

func (r *MockResourceOps) GetLastValidate(key kube.ResourceKey) bool {
	r.recordLock.RLock()
	validate := r.lastCommand(key).Validate
	r.recordLock.RUnlock()
	return validate
}

func (r *MockResourceOps) GetLastServerSideApplyManager(key kube.ResourceKey) string {
	r.recordLock.Lock()
	manager := r.lastCommand(key).ServerSideApplyManager
	r.recordLock.Unlock()
	return manager
}

func (r *MockResourceOps) GetLastServerSideApply(key kube.ResourceKey) bool {
	r.recordLock.RLock()
	serverSideApply := r.lastCommand(key).ServerSideApply
	r.recordLock.RUnlock()
	return serverSideApply
}

func (r *MockResourceOps) GetLastForce(key kube.ResourceKey) bool {
	r.recordLock.RLock()
	force := r.lastCommand(key).Force
	r.recordLock.RUnlock()
	return force
}

func (r *MockResourceOps) GetLastResourceCommand(key kube.ResourceKey) string {
	r.recordLock.Lock()
	defer r.recordLock.Unlock()
	if r.commandsPerResource == nil {
		return ""
	}
	return r.lastCommand(key).Command
}

func (r *MockResourceOps) RegisteredCommands(key kube.ResourceKey) []RegisteredCommand {
	r.recordLock.RLock()
	registeredCommands := r.commandsPerResource[key]
	r.recordLock.RUnlock()
	return registeredCommands
}

func (r *MockResourceOps) ApplyResource(ctx context.Context, obj *unstructured.Unstructured, dryRunStrategy cmdutil.DryRunStrategy, force, validate, serverSideApply bool, manager string, serverSideDiff bool) (string, error) {
	r.registerCommand(kube.GetResourceKey(obj), RegisteredCommand{
		Command: "apply",
		Validate: validate,
		ServerSideApply: serverSideApply,
		ServerSideApplyManager: manager,
		Force: force,
		DryRunStrategy: dryRunStrategy,
	})
	command, ok := r.Commands[obj.GetName()]
	if !ok {
		return "", nil
	}

	return command.Output, command.Err
}

func (r *MockResourceOps) ReplaceResource(ctx context.Context, obj *unstructured.Unstructured, dryRunStrategy cmdutil.DryRunStrategy, force bool) (string, error) {
	r.registerCommand(kube.GetResourceKey(obj), RegisteredCommand{
		Command: "replace",
		Force: force,
		DryRunStrategy: dryRunStrategy,
	})
	command, ok := r.Commands[obj.GetName()]
	if !ok {
		return "", nil
	}

	return command.Output, command.Err
}

func (r *MockResourceOps) UpdateResource(ctx context.Context, obj *unstructured.Unstructured, dryRunStrategy cmdutil.DryRunStrategy) (*unstructured.Unstructured, error) {
	r.registerCommand(kube.GetResourceKey(obj), RegisteredCommand{
		Command: "update",
		DryRunStrategy: dryRunStrategy,
	})
	command, ok := r.Commands[obj.GetName()]
	if !ok {
		return obj, nil
	}
	return obj, command.Err

}

func (r *MockResourceOps) CreateResource(ctx context.Context, obj *unstructured.Unstructured, dryRunStrategy cmdutil.DryRunStrategy, validate bool) (string, error) {
	r.registerCommand(kube.GetResourceKey(obj), RegisteredCommand{
		Command: "create",
		Validate: validate,
		DryRunStrategy: dryRunStrategy,
	})
	command, ok := r.Commands[obj.GetName()]
	if !ok {
		return "", nil
	}
	return command.Output, command.Err
}

func (r *MockResourceOps) registerCommand(key kube.ResourceKey, cmd RegisteredCommand) {
	r.recordLock.Lock()
	if r.commandsPerResource == nil {
		r.commandsPerResource = map[kube.ResourceKey][]RegisteredCommand{}
	}
	r.commandsPerResource[key] = append(r.commandsPerResource[key], cmd)
	r.recordLock.Unlock()
}

func (r *MockResourceOps) lastCommand(key kube.ResourceKey) RegisteredCommand {
	return r.commandsPerResource[key][len(r.commandsPerResource[key])-1]
}

/*func (r *MockResourceOps) ConvertToVersion(obj *unstructured.Unstructured, group, version string) (*unstructured.Unstructured, error) {
	if r.convertToVersionFunc != nil {
		return (*r.convertToVersionFunc)(obj, group, version)
	}

	return obj, nil
}*/
