package cache

import (
	"github.com/argoproj/gitops-engine/pkg/utils/kube"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sync"
)

// ApisMetaMap is thread-safe map of apiMeta
type ApisMetaMap struct {
	log     logr.Logger
	syncMap sync.Map
}

func (m *ApisMetaMap) Load(gk schema.GroupKind) (*apiMeta, bool) {
	val, ok := m.syncMap.Load(gk)
	typedVal, typeOk := val.(*apiMeta)
	if !ok || !typeOk {
		return nil, false
	}
	return typedVal, true
}

func (m *ApisMetaMap) LoadOrStore(key schema.GroupKind, val *apiMeta) (*apiMeta, bool) {
	actual, loaded := m.syncMap.LoadOrStore(key, val)
	if !loaded {
		return val, false
	}
	typedVal, typeOk := actual.(*apiMeta)
	if !typeOk {
		return nil, false
	}
	return typedVal, true
}

func (m *ApisMetaMap) Store(gk schema.GroupKind, meta *apiMeta) {
	m.syncMap.Store(gk, meta)
}

func (m *ApisMetaMap) Delete(gk schema.GroupKind) {
	m.syncMap.Delete(gk)
}

//Range loops the map, and Range ensures every item will be load, but not guarantee missing(phantom read)
func (m *ApisMetaMap) Range(fn func(key schema.GroupKind, value *apiMeta) bool) {
	m.syncMap.Range(func(key, value interface{}) bool {
		typedKey, keyTypeOk := key.(schema.GroupKind)
		typedValue, valueTypeOk := value.(*apiMeta)
		if !keyTypeOk || !valueTypeOk {
			m.log.Info("Failed to cast key and value to GroupKind and *apiMeta")
			return false
		}
		return fn(typedKey, typedValue)
	})
}

//Len return ApisMetaMap length, roughly, it depends on the time point of Range each loop
func (m *ApisMetaMap) Len() int {
	length := 0
	m.syncMap.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	return length
}

// ResourceMap is thread-safe map of kube.ResourceKey to *Resource
type ResourceMap struct {
	log     logr.Logger
	syncMap sync.Map
}

func (m *ResourceMap) Load(key kube.ResourceKey) (*Resource, bool) {
	val, ok := m.syncMap.Load(key)
	typedVal, typeOk := val.(*Resource)
	if !ok || !typeOk {
		return nil, false
	}
	return typedVal, true
}

func (m *ResourceMap) LoadAndDelete(key kube.ResourceKey) (*Resource, bool) {
	val, loaded := m.syncMap.LoadAndDelete(key)
	if !loaded {
		return nil, false
	}
	typedVal, typeOk := val.(*Resource)
	if !typeOk {
		m.log.Info("Failed to cast value to *Resource")
		return nil, true
	}
	return typedVal, true
}

func (m *ResourceMap) LoadOrStore(key kube.ResourceKey, val *Resource) (*Resource, bool) {
	actual, loaded := m.syncMap.LoadOrStore(key, val)
	if !loaded {
		return val, false
	}
	typedVal, typeOk := actual.(*Resource)
	if !typeOk {
		return nil, false
	}
	return typedVal, true
}

func (m *ResourceMap) Store(key kube.ResourceKey, resource *Resource) {
	m.syncMap.Store(key, resource)
}

func (m *ResourceMap) Delete(key kube.ResourceKey) {
	m.syncMap.Delete(key)
}

func (m *ResourceMap) Range(fn func(key kube.ResourceKey, value *Resource) bool) {
	m.syncMap.Range(func(key, value interface{}) bool {
		typedKey, keyTypeOk := key.(kube.ResourceKey)
		typedValue, valueTypeOk := value.(*Resource)
		if !keyTypeOk || !valueTypeOk {
			m.log.Info("Failed to cast key and value to kube.ResourceKey and *Resource")
			return false
		}
		return fn(typedKey, typedValue)
	})
}

func (m *ResourceMap) Len() int {
	length := 0
	m.syncMap.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	return length
}

// All return all of the original resources in the map, this maybe cause pointer leaks, deprecated.
// TODO remove
func (l *ResourceMap) All() map[kube.ResourceKey]*Resource {
	result := make(map[kube.ResourceKey]*Resource)
	l.Range(func(key kube.ResourceKey, value *Resource) bool {
		result[key] = value
		return true
	})
	return result
}

type APIGroupList struct {
	lock sync.RWMutex
	list []metav1.APIGroup
}

func (l *APIGroupList) Add(group metav1.APIGroup) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.list = append(l.list, group)
}

func (l *APIGroupList) Get() []metav1.APIGroup {
	l.lock.RLock()
	defer l.lock.RUnlock()
	return l.list
}

func (l *APIGroupList) Len() int {
	l.lock.RLock()
	defer l.lock.RUnlock()
	return len(l.list)
}

// Remove return true if item exited
func (l *APIGroupList) Remove(group metav1.APIGroup) bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	for i, v := range l.list {
		if v.Name == group.Name {
			l.list = append(l.list[:i], l.list[i+1:]...)
			return true
		}
	}
	return false
}

func (l *APIGroupList) Clear() {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.list = []metav1.APIGroup{}
}

// GetReferrerList return real inner list of APIGroupList, deprecated
func (l *APIGroupList) GetReferrerList() []metav1.APIGroup {
	return l.list
}

// All return all of the original resources in the map, this maybe cause pointer leaks, depreacated.
// TODO remove
func (l *APIGroupList) All() []metav1.APIGroup {
	snapshot := make([]metav1.APIGroup, len(l.list))
	copy(l.list, snapshot)
	return snapshot
}

// AddIfAbsent return true if added, deprecated O(N)
func (l *APIGroupList) AddIfAbsent(group metav1.APIGroup) bool {
	l.lock.Lock()
	defer l.lock.Unlock()
	for _, v := range l.list {
		if v.Name == group.Name {
			return false
		}
	}
	l.list = append(l.list, group)
	return true
}

// NamespaceResourcesMap is thread-safe map of string to *ResourceMap
type NamespaceResourcesMap struct {
	log     logr.Logger
	syncMap sync.Map
}

func (m *NamespaceResourcesMap) Load(key string) (*ResourceMap, bool) {
	val, ok := m.syncMap.Load(key)
	typedVal, typeOk := val.(*ResourceMap)
	if !ok || !typeOk {
		return nil, false
	}
	return typedVal, true
}

func (m *NamespaceResourcesMap) LoadOrStore(key string, val *ResourceMap) (*ResourceMap, bool) {
	actual, loaded := m.syncMap.LoadOrStore(key, val)
	if !loaded {
		return val, false
	}
	typedVal, typeOk := actual.(*ResourceMap)
	if !typeOk {
		return nil, false
	}
	return typedVal, true
}

func (m *NamespaceResourcesMap) Store(key string, resource *ResourceMap) {
	m.syncMap.Store(key, resource)
}

func (m *NamespaceResourcesMap) Delete(key string) {
	m.syncMap.Delete(key)
}

func (m *NamespaceResourcesMap) Range(fn func(key string, value *ResourceMap) bool) {
	m.syncMap.Range(func(key, value interface{}) bool {
		typedKey, keyTypeOk := key.(string)
		typedValue, valueTypeOk := value.(*ResourceMap)
		if !keyTypeOk || !valueTypeOk {
			m.log.Info("Failed to cast key and value to string and *ResourceMap")
			return false
		}
		return fn(typedKey, typedValue)
	})
}

func (m *NamespaceResourcesMap) Len() int {
	length := 0
	m.syncMap.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	return length
}

// GroupKindBoolMap is thread-safe map of schema.GroupKind to bool
type GroupKindBoolMap struct {
	log     logr.Logger
	syncMap sync.Map
}

func (m *GroupKindBoolMap) Load(key schema.GroupKind) (bool, bool) {
	val, ok := m.syncMap.Load(key)
	typedVal, typeOk := val.(bool)
	if !ok || !typeOk {
		return false, false
	}
	return typedVal, true
}

func (m *GroupKindBoolMap) LoadOrStore(key schema.GroupKind, val bool) (bool, bool) {
	actual, loaded := m.syncMap.LoadOrStore(key, val)
	if !loaded {
		return val, false
	}
	typedVal, typeOk := actual.(bool)
	if !typeOk {
		return false, false
	}
	return typedVal, true
}

func (m *GroupKindBoolMap) Store(key schema.GroupKind, resource bool) {
	m.syncMap.Store(key, resource)
}

func (m *GroupKindBoolMap) Delete(key schema.GroupKind) {
	m.syncMap.Delete(key)
}

func (m *GroupKindBoolMap) Range(fn func(key schema.GroupKind, value bool) bool) {
	m.syncMap.Range(func(key, value interface{}) bool {
		typedKey, keyTypeOk := key.(schema.GroupKind)
		typedValue, valueTypeOk := value.(bool)
		if !keyTypeOk || !valueTypeOk {
			m.log.Info("Failed to cast key and value to schema.GroupKind and bool")
			return false
		}
		return fn(typedKey, typedValue)
	})
}

func (m *GroupKindBoolMap) Len() int {
	length := 0
	m.syncMap.Range(func(_, _ interface{}) bool {
		length++
		return true
	})
	return length
}

func (m *GroupKindBoolMap) Reload(resources map[schema.GroupKind]bool) {
	m.syncMap.Range(func(key, value interface{}) bool {
		m.syncMap.Delete(key)
		return true
	})
	// TODO not atomic now, need to fix
	for kind, b := range resources {
		m.syncMap.Store(kind, b)
	}
}

type StringList struct {
	lock sync.RWMutex
	list []string
}

func (l *StringList) Add(s string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.list = append(l.list, s)
}

func (l *StringList) Remove(s string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	for i, v := range l.list {
		if v == s {
			l.list = append(l.list[:i], l.list[i+1:]...)
			return
		}
	}
}

func (l *StringList) Range(fn func(string) bool) {
	l.lock.RLock()
	defer l.lock.RUnlock()
	for _, v := range l.list {
		if !fn(v) {
			return
		}
	}
}

func (l *StringList) Contains(s string) bool {
	l.lock.RLock()
	defer l.lock.RUnlock()
	for _, v := range l.list {
		if v == s {
			return true
		}
	}
	return false
}

func (l *StringList) Len() int {
	l.lock.RLock()
	defer l.lock.RUnlock()
	return len(l.list)
}

func (l *StringList) List() []string {
	l.lock.RLock()
	defer l.lock.RUnlock()
	return l.list
}

func (l *StringList) Clear() {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.list = []string{}
}
