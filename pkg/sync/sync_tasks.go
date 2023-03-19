package sync

import (
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"sort"
	"strings"

	"github.com/argoproj/gitops-engine/pkg/sync/common"
)

// kindOrder represents the correct order of Kubernetes resources within a manifest
var syncPhaseOrder = map[common.SyncPhase]int{
	common.SyncPhasePreSync:  -1,
	common.SyncPhaseSync:     0,
	common.SyncPhasePostSync: 1,
	common.SyncPhaseSyncFail: 2,
}

// kindOrder represents the correct order of Kubernetes resources within a manifest
// https://github.com/helm/helm/blob/0361dc85689e3a6d802c444e2540c92cb5842bc9/pkg/releaseutil/kind_sorter.go
var kindOrder = map[string]int{}

func init() {
	kinds := []string{
		"Namespace",
		"NetworkPolicy",
		"ResourceQuota",
		"LimitRange",
		"PodSecurityPolicy",
		"PodDisruptionBudget",
		"ServiceAccount",
		"Secret",
		"SecretList",
		"ConfigMap",
		"StorageClass",
		"PersistentVolume",
		"PersistentVolumeClaim",
		"CustomResourceDefinition",
		"ClusterRole",
		"ClusterRoleList",
		"ClusterRoleBinding",
		"ClusterRoleBindingList",
		"Role",
		"RoleList",
		"RoleBinding",
		"RoleBindingList",
		"Service",
		"DaemonSet",
		"Pod",
		"ReplicationController",
		"ReplicaSet",
		"Deployment",
		"HorizontalPodAutoscaler",
		"StatefulSet",
		"Job",
		"CronJob",
		"IngressClass",
		"Ingress",
		"APIService",
	}
	for i, kind := range kinds {
		// make sure none of the above entries are zero, we need that for custom resources
		kindOrder[kind] = i - len(kinds)
	}
}

type syncTasks []*syncTask

func (s syncTasks) Len() int {
	return len(s)
}

func (s syncTasks) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// Less returns true if task i should be sorted before task j
// The order is:
// 1. Namespaces
// 2. CRDs
// 3. Sync phase
// 4. Wave
// 5. Kind
// 6. Name
func (s syncTasks) Less(i, j int) bool {

	l := s[i]
	r := s[j]

	a := l.obj()
	b := r.obj()

	// namespaces must come before objects that depend on them
	if a.GetKind() == kube.NamespaceKind && a.GroupVersionKind().Group == "" && a.GetName() == b.GetNamespace() {
		return true
	}

	// crds must come before objects that depend on them
	if isCRDOfGroupKind(b.GroupVersionKind().Group, b.GetKind(), a) {
		return true
	}

	// Order by dependency.
	// We tolerate cycles in the dependency graph, but we will not detect them.
	// We also tolerate missing dependencies, but we will not detect them.
	deps := r.dependencies()
	for len(deps) > 0 {
		dep := s.taskFor(deps[0])
		deps = deps[1:]
		if dep == nil {
			continue
		}
		if dep == l {
			return true
		}
		deps = append(deps, dep.dependencies()...)
	}

	d := syncPhaseOrder[l.phase] - syncPhaseOrder[r.phase]
	if d != 0 {
		return d < 0
	}

	d = l.wave() - r.wave()
	if d != 0 {
		return d < 0
	}

	// we take advantage of the fact that if the kind is not in the kindOrder map,
	// then it will return the default int value of zero, which is the highest value
	d = kindOrder[a.GetKind()] - kindOrder[b.GetKind()]
	if d != 0 {
		return d < 0
	}

	return a.GetName() < b.GetName()
}

func (s syncTasks) Sort() {
	sort.Sort(s)
}

func (s syncTasks) Filter(predicate func(task *syncTask) bool) (tasks syncTasks) {
	for _, task := range s {
		if predicate(task) {
			tasks = append(tasks, task)
		}
	}
	return tasks
}

func (s syncTasks) Split(predicate func(task *syncTask) bool) (trueTasks, falseTasks syncTasks) {
	for _, task := range s {
		if predicate(task) {
			trueTasks = append(trueTasks, task)
		} else {
			falseTasks = append(falseTasks, task)
		}
	}
	return trueTasks, falseTasks
}

func (s syncTasks) Map(predicate func(task *syncTask) string) []string {
	messagesMap := make(map[string]interface{})
	for _, task := range s {
		messagesMap[predicate(task)] = nil
	}
	messages := make([]string, 0)
	for key := range messagesMap {
		messages = append(messages, key)
	}
	return messages
}

func (s syncTasks) All(predicate func(task *syncTask) bool) bool {
	for _, task := range s {
		if !predicate(task) {
			return false
		}
	}
	return true
}

func (s syncTasks) Any(predicate func(task *syncTask) bool) bool {
	for _, task := range s {
		if predicate(task) {
			return true
		}
	}
	return false
}

func (s syncTasks) Find(predicate func(task *syncTask) bool) *syncTask {
	for _, task := range s {
		if predicate(task) {
			return task
		}
	}
	return nil
}

func (s syncTasks) String() string {
	var values []string
	for _, task := range s {
		values = append(values, task.String())
	}
	return "[" + strings.Join(values, ", ") + "]"
}

func (s syncTasks) names() []string {
	var values []string
	for _, task := range s {
		values = append(values, task.name())
	}
	return values
}

func (s syncTasks) phase() common.SyncPhase {
	if len(s) > 0 {
		return s[0].phase
	}
	return ""
}

func (s syncTasks) wave() int {
	if len(s) > 0 {
		return s[0].wave()
	}
	return 0
}

func (s syncTasks) lastPhase() common.SyncPhase {
	if len(s) > 0 {
		return s[len(s)-1].phase
	}
	return ""
}

func (s syncTasks) lastWave() int {
	if len(s) > 0 {
		return s[len(s)-1].wave()
	}
	return 0
}

func (s syncTasks) multiStep() bool {
	return s.wave() != s.lastWave() || s.phase() != s.lastPhase() || s.Any(func(task *syncTask) bool {
		return len(task.dependencies()) > 0
	})
}

func (s syncTasks) taskFor(dep taskDependency) *syncTask {
	return s.Find(func(task *syncTask) bool {
		return dep.match(task.obj())
	})
}
