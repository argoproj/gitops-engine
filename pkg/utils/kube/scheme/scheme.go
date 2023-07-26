package scheme

import (
	"k8s.io/apimachinery/pkg/runtime"
	runtimeutil "k8s.io/apimachinery/pkg/util/runtime"

	admissionv1 "k8s.io/api/admission/v1"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	appsv1beta1 "k8s.io/api/apps/v1beta1"
	appsv1beta2 "k8s.io/api/apps/v1beta2"
	authenticationv1 "k8s.io/api/authentication/v1"
	authenticationv1beta1 "k8s.io/api/authentication/v1beta1"
	authorizationv1 "k8s.io/api/authorization/v1"
	authorizationv1beta1 "k8s.io/api/authorization/v1beta1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	autoscalingv2beta1 "k8s.io/api/autoscaling/v2beta1"
	autoscalingv2beta2 "k8s.io/api/autoscaling/v2beta2"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	certificatesv1 "k8s.io/api/certificates/v1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	coordinationv1 "k8s.io/api/coordination/v1"
	coordinationv1beta1 "k8s.io/api/coordination/v1beta1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	discoveryv1beta1 "k8s.io/api/discovery/v1beta1"
	eventsv1 "k8s.io/api/events/v1"
	eventsv1beta1 "k8s.io/api/events/v1beta1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	flowcontrolv1alpha1 "k8s.io/api/flowcontrol/v1alpha1"
	flowcontrolv1beta1 "k8s.io/api/flowcontrol/v1beta1"
	flowcontrolv1beta2 "k8s.io/api/flowcontrol/v1beta2"
	imagepolicyv1alpha1 "k8s.io/api/imagepolicy/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	nodev1 "k8s.io/api/node/v1"
	nodev1alpha1 "k8s.io/api/node/v1alpha1"
	nodev1beta1 "k8s.io/api/node/v1beta1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	rbacv1alpha1 "k8s.io/api/rbac/v1alpha1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	schedulingv1alpha1 "k8s.io/api/scheduling/v1alpha1"
	schedulingv1beta1 "k8s.io/api/scheduling/v1beta1"
	storagev1 "k8s.io/api/storage/v1"
	storagev1alpha1 "k8s.io/api/storage/v1alpha1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
)

var Scheme = runtime.NewScheme()

func init() {
	runtimeutil.Must(admissionv1.AddToScheme(Scheme))
	runtimeutil.Must(admissionv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(admissionregistrationv1.AddToScheme(Scheme))
	runtimeutil.Must(admissionregistrationv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(appsv1.AddToScheme(Scheme))
	runtimeutil.Must(appsv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(appsv1beta2.AddToScheme(Scheme))
	runtimeutil.Must(authenticationv1.AddToScheme(Scheme))
	runtimeutil.Must(authenticationv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(authorizationv1.AddToScheme(Scheme))
	runtimeutil.Must(authorizationv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(autoscalingv2.AddToScheme(Scheme))
	runtimeutil.Must(autoscalingv2beta2.AddToScheme(Scheme))
	runtimeutil.Must(autoscalingv2beta1.AddToScheme(Scheme))
	runtimeutil.Must(autoscalingv1.AddToScheme(Scheme))
	runtimeutil.Must(batchv1.AddToScheme(Scheme))
	runtimeutil.Must(batchv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(certificatesv1.AddToScheme(Scheme))
	runtimeutil.Must(certificatesv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(coordinationv1.AddToScheme(Scheme))
	runtimeutil.Must(coordinationv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(corev1.AddToScheme(Scheme))
	runtimeutil.Must(discoveryv1.AddToScheme(Scheme))
	runtimeutil.Must(discoveryv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(eventsv1.AddToScheme(Scheme))
	runtimeutil.Must(eventsv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(extensionsv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(flowcontrolv1alpha1.AddToScheme(Scheme))
	runtimeutil.Must(flowcontrolv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(flowcontrolv1beta2.AddToScheme(Scheme))
	runtimeutil.Must(imagepolicyv1alpha1.AddToScheme(Scheme))
	runtimeutil.Must(networkingv1.AddToScheme(Scheme))
	runtimeutil.Must(networkingv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(nodev1.AddToScheme(Scheme))
	runtimeutil.Must(nodev1beta1.AddToScheme(Scheme))
	runtimeutil.Must(nodev1alpha1.AddToScheme(Scheme))
	runtimeutil.Must(policyv1.AddToScheme(Scheme))
	runtimeutil.Must(policyv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(rbacv1.AddToScheme(Scheme))
	runtimeutil.Must(rbacv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(rbacv1alpha1.AddToScheme(Scheme))
	runtimeutil.Must(schedulingv1.AddToScheme(Scheme))
	runtimeutil.Must(schedulingv1beta1.AddToScheme(Scheme))
	runtimeutil.Must(schedulingv1alpha1.AddToScheme(Scheme))
	runtimeutil.Must(storagev1.AddToScheme(Scheme))
	runtimeutil.Must(storagev1beta1.AddToScheme(Scheme))
	runtimeutil.Must(storagev1alpha1.AddToScheme(Scheme))
}
