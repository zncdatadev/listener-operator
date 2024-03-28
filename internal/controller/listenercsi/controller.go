/*
Copyright 2024 zncdata-labs.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package listenercsi

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	listenersv1alpha1 "github.com/zncdata-labs/listener-operator/api/v1alpha1"
)

var (
	logger = ctrl.Log.WithName("listenercsi")
)

// ListenerCSIReconciler reconciles a ListenerCSI object
type ListenerCSIReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listenercsis,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listenercsis/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listenercsis/finalizers,verbs=update
//+kubebuilder:rbac:groups=listeners.zncdata.dev,resources=listenerclasses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ListenerCSI object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *ListenerCSIReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	instance := &listenersv1alpha1.ListenerCSI{}

	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logger.V(5).Info("ListenerCSI resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ListenerCSI")
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Reconciling ListenerCSI", "Name", instance.Name)

	if result, err := NewCSIDriver(r.Client, instance).Reconcile(ctx); err != nil {
		return result, err
	} else if result.Requeue {
		return result, nil
	}

	if result, err := NewRBAC(r.Client, instance).Reconcile(ctx); err != nil {
		return result, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	if result, err := NewStorageClass(r.Client, instance).Reconcile(ctx); err != nil {
		return result, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	if result, err := NewPersetListenerClass(r.Client, instance).Reconcile(ctx); err != nil {
		return result, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	daemonSet := NewDaemonSet(r.Client, instance, CSIServiceAccountName)

	if result, err := daemonSet.Reconcile(ctx); err != nil {
		return result, err
	} else if result.RequeueAfter > 0 {
		return result, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ListenerCSIReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&listenersv1alpha1.ListenerCSI{}).
		Complete(r)
}
