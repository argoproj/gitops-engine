package sync

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/argoproj/gitops-engine/pkg/sync/common"
	"github.com/argoproj/gitops-engine/pkg/sync/syncwaves"
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
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

// order is
// 1. phase
// 2. wave
// 3. kind
// 4. name
func (s syncTasks) Less(i, j int) bool {
	tA := s[i]
	tB := s[j]

	d := syncPhaseOrder[tA.phase] - syncPhaseOrder[tB.phase]
	if d != 0 {
		return d < 0
	}

	d = tA.wave() - tB.wave()
	if d != 0 {
		return d < 0
	}

	a := tA.obj()
	b := tB.obj()

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
	// make sure namespaces are created before resources referencing namespaces
	s.adjustDeps(func(obj *unstructured.Unstructured) (string, bool) {
		return obj.GetName(), obj.GetKind() == kube.NamespaceKind && obj.GroupVersionKind().Group == ""
	}, func(obj *unstructured.Unstructured) (string, bool) {
		return obj.GetNamespace(), obj.GetNamespace() != ""
	})
	// make sure CRDs are created before CRs
	s.adjustDeps(func(obj *unstructured.Unstructured) (string, bool) {
		if kube.IsCRD(obj) {
			crdGroup, ok, err := unstructured.NestedString(obj.Object, "spec", "group")
			if err != nil || !ok {
				return "", false
			}
			crdKind, ok, err := unstructured.NestedString(obj.Object, "spec", "names", "kind")
			if err != nil || !ok {
				return "", false
			}
			return fmt.Sprintf("%s/%s", crdGroup, crdKind), true
		}
		return "", false
	}, func(obj *unstructured.Unstructured) (string, bool) {
		gk := obj.GroupVersionKind()
		return fmt.Sprintf("%s/%s", gk.Group, gk.Kind), true
	})
}

func LessUsingBinaryTreeOrdering(i int, j int) bool {
	if (i <= 1) && (j <= 1) {
		return i < j
	}
	if i <= 1 {
		return true
	}
	if j <= 1 {
		return false
	}

	N := 2
	for j-int(N)*i >= 0 {
		if j-N*i < N {
			return true
		}
		N *= 2
	}
	return false
}

func biggestPowerOf2InferiorThan(n int) int {
	if n < 1 {
		return 0
	}
	i := 1
	for i <= n {
		i = i * 2
	}
	return i / 2
}

func PowInt(n int, m int) int {
	result := 1
	for i := 1; i <= m; i++ {
		result = result * n
	}
	return result
}

// adjust order of tasks and bubble up tasks which are dependencies of other tasks
// (e.g. namespace sync should happen before resources that resides in that namespace)
func (s syncTasks) adjustDeps(isDep func(obj *unstructured.Unstructured) (string, bool), doesRefDep func(obj *unstructured.Unstructured) (string, bool)) {
	// store dependency key and first occurrence of resource referencing the dependency
	firstIndexByDepKey := map[string]int{}

	for i, t := range s {
		if t.targetObj == nil {
			continue
		}

		if depKey, ok := isDep(t.targetObj); ok {
			// if tasks is a dependency then insert if before first task that reference it
			if index, ok := firstIndexByDepKey[depKey]; ok {
				// wave and sync phase of dependency resource must be same as wave and phase of resource that depend on it
				wave := s[index].wave()
				t.waveOverride = &wave
				t.phase = s[index].phase

				for j := i; j > index; j-- {
					s[j] = s[j-1]
				}
				s[index] = t
				// increase previously collected indexes by 1
				for ns, firstIndex := range firstIndexByDepKey {
					if firstIndex >= index {
						firstIndexByDepKey[ns] = firstIndex + 1
					}
				}
			}
		} else if depKey, ok := doesRefDep(t.targetObj); ok {
			// if task is referencing the dependency then store first index of it
			if _, ok := firstIndexByDepKey[depKey]; !ok {
				firstIndexByDepKey[depKey] = i
			}
		}
	}
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
	messagesMap := make(map[string]any)
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

func (s syncTasks) phase() common.SyncPhase {
	if len(s) > 0 {
		return s[0].phase
	}
	return ""
}

func (s syncTasks) waves() ([]int, string) {
	var wavesValues []int
	tasksUsingNormalWaveOrdering := s.Filter(func(t *syncTask) bool { return syncwaves.UseBinaryTreeWaveOrdering(t.obj()) == "false" })
	tasksUsingBinaryTreeWaveOrdering := s.Filter(func(t *syncTask) bool { return syncwaves.UseBinaryTreeWaveOrdering(t.obj()) == "true" })
	if len(tasksUsingNormalWaveOrdering) > 0 {
		wavesValues = append(wavesValues, tasksUsingNormalWaveOrdering[0].wave())
		return wavesValues, "false"
	}
	if len(tasksUsingBinaryTreeWaveOrdering) > 0 {
		for iSyncTask := range tasksUsingBinaryTreeWaveOrdering {
			candidateTask := tasksUsingBinaryTreeWaveOrdering[iSyncTask]
			candidateTaskHasNoAntecedent := true
			for jSyncTask := range tasksUsingBinaryTreeWaveOrdering {
				task := tasksUsingBinaryTreeWaveOrdering[jSyncTask]
				if LessUsingBinaryTreeOrdering(syncwaves.Wave(task.obj()), syncwaves.Wave(candidateTask.obj())) {
					candidateTaskHasNoAntecedent = false
					break
				}
			}
			if candidateTaskHasNoAntecedent {
				wavesValues = append(wavesValues, syncwaves.Wave(candidateTask.obj()))
			}
		}
		return wavesValues, "true"
	}
	wavesValues = append(wavesValues, 0)
	return wavesValues, "false"
}

func (s syncTasks) lastPhase() common.SyncPhase {
	if len(s) > 0 {
		return s[len(s)-1].phase
	}
	return ""
}

func (s syncTasks) lastWaves() ([]int, string) {
	tasksUsingNormalWaveOrdering := s.Filter(func(t *syncTask) bool { return syncwaves.UseBinaryTreeWaveOrdering(t.obj()) == "false" })
	tasksUsingBinaryTreeWaveOrdering := s.Filter(func(t *syncTask) bool { return syncwaves.UseBinaryTreeWaveOrdering(t.obj()) == "true" })

	var lastWavesValues []int

	if len(tasksUsingBinaryTreeWaveOrdering) > 0 {
		for iSyncTask := range tasksUsingBinaryTreeWaveOrdering {
			candidateTask := s[iSyncTask]
			candidateTaskHasNoSuccessor := true
			for jSyncTask := range s {
				task := s[jSyncTask]
				if LessUsingBinaryTreeOrdering(syncwaves.Wave(candidateTask.obj()), syncwaves.Wave(task.obj())) {
					candidateTaskHasNoSuccessor = false
					break
				}
			}
			if candidateTaskHasNoSuccessor {
				lastWavesValues = append(lastWavesValues, syncwaves.Wave(candidateTask.obj()))
			}
		}
		return lastWavesValues, "true"
	}

	if len(tasksUsingNormalWaveOrdering) > 0 {
		lastWavesValues = append(lastWavesValues, tasksUsingNormalWaveOrdering[len(tasksUsingNormalWaveOrdering)-1].wave())
		return lastWavesValues, "false"
	}

	lastWavesValues = append(lastWavesValues, 0)
	return lastWavesValues, "false"
}

func (s syncTasks) multiStep() bool {
	wavesValues, wavesUseBinaryTreeOrdering := s.waves()
	lastWavesValues, lastWavesUseBinaryTreeOrdering := s.lastWaves()
	return !reflect.DeepEqual(wavesValues, lastWavesValues) || wavesUseBinaryTreeOrdering != lastWavesUseBinaryTreeOrdering || s.phase() != s.lastPhase()
}
