// Code generated by mockery v2.14.1. DO NOT EDIT.

package mocks

import (
	cache "github.com/argoproj/gitops-engine/pkg/cache"
	kube "github.com/argoproj/gitops-engine/pkg/utils/kube"

	managedfields "k8s.io/apimachinery/pkg/util/managedfields"

	mock "github.com/stretchr/testify/mock"

	openapi "k8s.io/kubectl/pkg/util/openapi"

	schema "k8s.io/apimachinery/pkg/runtime/schema"

	unstructured "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ClusterCache is an autogenerated mock type for the ClusterCache type
type ClusterCache struct {
	mock.Mock
}

// EnsureSynced provides a mock function with given fields:
func (_m *ClusterCache) EnsureSynced() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// FindResources provides a mock function with given fields: namespace, predicates
func (_m *ClusterCache) FindResources(namespace string, predicates ...func(*cache.Resource) bool) map[kube.ResourceKey]*cache.Resource {
	_va := make([]interface{}, len(predicates))
	for _i := range predicates {
		_va[_i] = predicates[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, namespace)
	_ca = append(_ca, _va...)
	ret := _m.Called(_ca...)

	var r0 map[kube.ResourceKey]*cache.Resource
	if rf, ok := ret.Get(0).(func(string, ...func(*cache.Resource) bool) map[kube.ResourceKey]*cache.Resource); ok {
		r0 = rf(namespace, predicates...)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[kube.ResourceKey]*cache.Resource)
		}
	}

	return r0
}

// GetAPIResources provides a mock function with given fields:
func (_m *ClusterCache) GetAPIResources() []kube.APIResourceInfo {
	ret := _m.Called()

	var r0 []kube.APIResourceInfo
	if rf, ok := ret.Get(0).(func() []kube.APIResourceInfo); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]kube.APIResourceInfo)
		}
	}

	return r0
}

// GetClusterInfo provides a mock function with given fields:
func (_m *ClusterCache) GetClusterInfo() cache.ClusterInfo {
	ret := _m.Called()

	var r0 cache.ClusterInfo
	if rf, ok := ret.Get(0).(func() cache.ClusterInfo); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(cache.ClusterInfo)
	}

	return r0
}

// GetClusterInfoInstant provides a mock function with given fields:
func (_m *ClusterCache) GetClusterInfoInstant() cache.ClusterInfo {
	ret := _m.Called()

	var r0 cache.ClusterInfo
	if rf, ok := ret.Get(0).(func() cache.ClusterInfo); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(cache.ClusterInfo)
	}

	return r0
}

// GetGVKParser provides a mock function with given fields:
func (_m *ClusterCache) GetGVKParser() *managedfields.GvkParser {
	ret := _m.Called()

	var r0 *managedfields.GvkParser
	if rf, ok := ret.Get(0).(func() *managedfields.GvkParser); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*managedfields.GvkParser)
		}
	}

	return r0
}

// GetManagedLiveObjs provides a mock function with given fields: targetObjs, isManaged
func (_m *ClusterCache) GetManagedLiveObjs(targetObjs []*unstructured.Unstructured, isManaged func(*cache.Resource) bool) (map[kube.ResourceKey]*unstructured.Unstructured, error) {
	ret := _m.Called(targetObjs, isManaged)

	var r0 map[kube.ResourceKey]*unstructured.Unstructured
	if rf, ok := ret.Get(0).(func([]*unstructured.Unstructured, func(*cache.Resource) bool) map[kube.ResourceKey]*unstructured.Unstructured); ok {
		r0 = rf(targetObjs, isManaged)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(map[kube.ResourceKey]*unstructured.Unstructured)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func([]*unstructured.Unstructured, func(*cache.Resource) bool) error); ok {
		r1 = rf(targetObjs, isManaged)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// GetOpenAPISchema provides a mock function with given fields:
func (_m *ClusterCache) GetOpenAPISchema() openapi.Resources {
	ret := _m.Called()

	var r0 openapi.Resources
	if rf, ok := ret.Get(0).(func() openapi.Resources); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(openapi.Resources)
		}
	}

	return r0
}

// GetServerVersion provides a mock function with given fields:
func (_m *ClusterCache) GetServerVersion() string {
	ret := _m.Called()

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// Invalidate provides a mock function with given fields: opts
func (_m *ClusterCache) Invalidate(opts ...cache.UpdateSettingsFunc) {
	_va := make([]interface{}, len(opts))
	for _i := range opts {
		_va[_i] = opts[_i]
	}
	var _ca []interface{}
	_ca = append(_ca, _va...)
	_m.Called(_ca...)
}

// IsNamespaced provides a mock function with given fields: gk
func (_m *ClusterCache) IsNamespaced(gk schema.GroupKind) (bool, error) {
	ret := _m.Called(gk)

	var r0 bool
	if rf, ok := ret.Get(0).(func(schema.GroupKind) bool); ok {
		r0 = rf(gk)
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(schema.GroupKind) error); ok {
		r1 = rf(gk)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IterateHierarchy provides a mock function with given fields: key, action
func (_m *ClusterCache) IterateHierarchy(key kube.ResourceKey, action func(*cache.Resource, map[kube.ResourceKey]*cache.Resource) bool) {
	_m.Called(key, action)
}

// OnEvent provides a mock function with given fields: handler
func (_m *ClusterCache) OnEvent(handler cache.OnEventHandler) cache.Unsubscribe {
	ret := _m.Called(handler)

	var r0 cache.Unsubscribe
	if rf, ok := ret.Get(0).(func(cache.OnEventHandler) cache.Unsubscribe); ok {
		r0 = rf(handler)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(cache.Unsubscribe)
		}
	}

	return r0
}

// OnResourceUpdated provides a mock function with given fields: handler
func (_m *ClusterCache) OnResourceUpdated(handler cache.OnResourceUpdatedHandler) cache.Unsubscribe {
	ret := _m.Called(handler)

	var r0 cache.Unsubscribe
	if rf, ok := ret.Get(0).(func(cache.OnResourceUpdatedHandler) cache.Unsubscribe); ok {
		r0 = rf(handler)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(cache.Unsubscribe)
		}
	}

	return r0
}

type mockConstructorTestingTNewClusterCache interface {
	mock.TestingT
	Cleanup(func())
}

// NewClusterCache creates a new instance of ClusterCache. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewClusterCache(t mockConstructorTestingTNewClusterCache) *ClusterCache {
	mock := &ClusterCache{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
