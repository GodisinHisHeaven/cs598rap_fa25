package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	storagev1 "github.com/cs598rap/raft-kubernetes/operator/api/v1"
)

// RaftClusterReconciler reconciles a RaftCluster object
type RaftClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=storage.sys,resources=raftclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.sys,resources=raftclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=storage.sys,resources=raftclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile implements the reconciliation loop for RaftCluster
func (r *RaftClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the RaftCluster instance
	var cluster storagev1.RaftCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get RaftCluster")
		return ctrl.Result{}, err
	}

	// Initialize status if needed
	if cluster.Status.Phase == "" {
		cluster.Status.Phase = storagev1.PhasePending
		cluster.Status.JointState = storagev1.JointStateNone
		cluster.Status.ConfigurationIndex = 0
		if err := r.Status().Update(ctx, &cluster); err != nil {
			logger.Error(err, "Failed to initialize status")
			return ctrl.Result{}, err
		}
	}

	// Ensure headless service exists
	if err := r.reconcileService(ctx, &cluster); err != nil {
		logger.Error(err, "Failed to reconcile service")
		return ctrl.Result{}, err
	}

	// Ensure StatefulSet exists
	if err := r.reconcileStatefulSet(ctx, &cluster); err != nil {
		logger.Error(err, "Failed to reconcile StatefulSet")
		return ctrl.Result{}, err
	}

	// Check if reconfiguration is needed
	needsReconfig, err := r.needsReconfiguration(ctx, &cluster)
	if err != nil {
		logger.Error(err, "Failed to check reconfiguration need")
		return ctrl.Result{}, err
	}

	if needsReconfig {
		// Execute reconfiguration workflow based on safety mode
		result, err := r.reconcileReconfiguration(ctx, &cluster)
		if err != nil {
			logger.Error(err, "Reconfiguration failed")
			cluster.Status.Phase = storagev1.PhaseDegraded
			r.Status().Update(ctx, &cluster)
			return result, err
		}
		return result, nil
	}

	// Update status to Ready if not reconfiguring
	if cluster.Status.Phase != storagev1.PhaseReady {
		cluster.Status.Phase = storagev1.PhaseReady
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepNone
		if err := r.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// reconcileService ensures the headless service exists
func (r *RaftClusterReconciler) reconcileService(ctx context.Context, cluster *storagev1.RaftCluster) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-raft",
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		// Set owner reference
		if err := controllerutil.SetControllerReference(cluster, svc, r.Scheme); err != nil {
			return err
		}

		svc.Spec = corev1.ServiceSpec{
			ClusterIP: "None", // Headless service
			Selector: map[string]string{
				"app":     "raft-kv",
				"cluster": cluster.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name: "raft",
					Port: 9000,
				},
				{
					Name: "admin",
					Port: 9090,
				},
				{
					Name: "api",
					Port: 8080,
				},
			},
		}

		return nil
	})

	return err
}

// reconcileStatefulSet ensures the StatefulSet exists and is up to date
func (r *RaftClusterReconciler) reconcileStatefulSet(ctx context.Context, cluster *storagev1.RaftCluster) error {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		// Set owner reference
		if err := controllerutil.SetControllerReference(cluster, sts, r.Scheme); err != nil {
			return err
		}

		// Build initial peers list
		peers := ""
		for i := int32(0); i < cluster.Spec.Replicas; i++ {
			if i > 0 {
				peers += ","
			}
			peers += fmt.Sprintf("%s-%d.%s-raft:9000", cluster.Name, i, cluster.Name)
		}

		sts.Spec = appsv1.StatefulSetSpec{
			Replicas: &cluster.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":     "raft-kv",
					"cluster": cluster.Name,
				},
			},
			ServiceName: cluster.Name + "-raft",
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":     "raft-kv",
						"cluster": cluster.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "raft-kv",
							Image: cluster.Spec.Image,
							Args: []string{
								"-raft-addr=:9000",
								"-api-addr=:8080",
								"-admin-addr=:9090",
								"-peers=" + peers,
								fmt.Sprintf("-safety-mode=%s", cluster.Spec.SafetyMode),
								fmt.Sprintf("-election-timeout=%d", cluster.Spec.ElectionTimeoutMs),
							},
							Ports: []corev1.ContainerPort{
								{Name: "raft", ContainerPort: 9000},
								{Name: "api", ContainerPort: 8080},
								{Name: "admin", ContainerPort: 9090},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: "/data",
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: parseQuantity(cluster.Spec.StorageSize),
							},
						},
					},
				},
			},
		}

		return nil
	})

	return err
}

// needsReconfiguration checks if reconfiguration is needed
func (r *RaftClusterReconciler) needsReconfiguration(ctx context.Context, cluster *storagev1.RaftCluster) (bool, error) {
	// Get current StatefulSet
	var sts appsv1.StatefulSet
	if err := r.Get(ctx, types.NamespacedName{
		Name:      cluster.Name,
		Namespace: cluster.Namespace,
	}, &sts); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	// Check if replicas differ
	if sts.Spec.Replicas == nil {
		return false, nil
	}

	return *sts.Spec.Replicas != cluster.Spec.Replicas, nil
}

// reconcileReconfiguration handles the reconfiguration workflow
func (r *RaftClusterReconciler) reconcileReconfiguration(ctx context.Context, cluster *storagev1.RaftCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Check cooldown period
	if cluster.Status.LastReconfigAt != nil {
		cooldown := time.Duration(cluster.Spec.ReconfigPolicy.CooldownSeconds) * time.Second
		if time.Since(cluster.Status.LastReconfigAt.Time) < cooldown {
			logger.Info("In cooldown period, deferring reconfiguration")
			return ctrl.Result{RequeueAfter: cooldown}, nil
		}
	}

	cluster.Status.Phase = storagev1.PhaseReconfiguring

	// Dispatch based on safety mode
	switch cluster.Spec.SafetyMode {
	case "safe":
		return r.reconfigureSafe(ctx, cluster)
	case "unsafe-early-vote":
		return r.reconfigureUnsafeEarlyVote(ctx, cluster)
	case "unsafe-no-joint":
		return r.reconfigureUnsafeNoJoint(ctx, cluster)
	default:
		return r.reconfigureSafe(ctx, cluster)
	}
}

// reconfigureSafe implements the safe Joint Consensus workflow
func (r *RaftClusterReconciler) reconfigureSafe(ctx context.Context, cluster *storagev1.RaftCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Implement 6-step Joint Consensus workflow
	switch cluster.Status.CurrentReconfigStep {
	case storagev1.ReconfigStepNone:
		// Step 1: AddLearner
		logger.Info("Step 1: Adding learner")
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepAddLearner
		cluster.Status.JointState = storagev1.JointStateNone
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case storagev1.ReconfigStepAddLearner:
		// Step 2: CatchUp
		logger.Info("Step 2: Waiting for catch-up")
		// TODO: Check if learner has caught up via admin RPC
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepCatchUp
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

	case storagev1.ReconfigStepCatchUp:
		// Step 3: EnterJoint
		logger.Info("Step 3: Entering joint consensus")
		cluster.Status.JointState = storagev1.JointStateEntering
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepEnterJoint
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case storagev1.ReconfigStepEnterJoint:
		// Step 4: CommitNew
		logger.Info("Step 4: Committing new configuration")
		cluster.Status.JointState = storagev1.JointStateCommitted
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepCommitNew
		cluster.Status.ConfigurationIndex++
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case storagev1.ReconfigStepCommitNew:
		// Step 5: LeaveJoint
		logger.Info("Step 5: Leaving joint consensus")
		cluster.Status.JointState = storagev1.JointStateLeaving
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepLeaveJoint
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil

	case storagev1.ReconfigStepLeaveJoint:
		// Step 6: Complete
		logger.Info("Step 6: Reconfiguration complete")
		cluster.Status.JointState = storagev1.JointStateNone
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepNone
		cluster.Status.Phase = storagev1.PhaseReady
		now := metav1.Now()
		cluster.Status.LastReconfigAt = &now
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil

	default:
		// Reset to beginning if in unknown state
		cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepNone
		if err := r.Status().Update(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
}

// reconfigureUnsafeEarlyVote implements Bug 1: Early-Vote Learner
func (r *RaftClusterReconciler) reconfigureUnsafeEarlyVote(ctx context.Context, cluster *storagev1.RaftCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("UNSAFE: Adding voter immediately without learner stage")

	// Skip learner stage - add as voter immediately
	// This is the bug: new node can vote before catching up

	cluster.Status.Phase = storagev1.PhaseReady
	cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepNone
	now := metav1.Now()
	cluster.Status.LastReconfigAt = &now
	if err := r.Status().Update(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// reconfigureUnsafeNoJoint implements Bug 2: No Joint Consensus
func (r *RaftClusterReconciler) reconfigureUnsafeNoJoint(ctx context.Context, cluster *storagev1.RaftCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("UNSAFE: Skipping joint consensus")

	// Skip joint consensus - apply new configuration directly
	// This is the bug: Old and New quorums may not intersect

	cluster.Status.Phase = storagev1.PhaseReady
	cluster.Status.CurrentReconfigStep = storagev1.ReconfigStepNone
	cluster.Status.JointState = storagev1.JointStateNone
	now := metav1.Now()
	cluster.Status.LastReconfigAt = &now
	if err := r.Status().Update(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// parseQuantity parses a resource quantity string
func parseQuantity(s string) resource.Quantity {
	// Simplified - in production use resource.MustParse
	q, _ := resource.ParseQuantity(s)
	return q
}

// SetupWithManager sets up the controller with the Manager.
func (r *RaftClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1.RaftCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
