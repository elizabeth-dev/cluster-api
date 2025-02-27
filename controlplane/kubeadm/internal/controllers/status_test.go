/*
Copyright 2020 The Kubernetes Authors.

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

package controllers

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	bootstrapv1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/etcd"
	controlplanev1webhooks "sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/webhooks"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
	v1beta2conditions "sigs.k8s.io/cluster-api/util/conditions/v1beta2"
)

func TestSetReplicas(t *testing.T) {
	g := NewWithT(t)
	readyTrue := metav1.Condition{Type: clusterv1.MachineReadyV1Beta2Condition, Status: metav1.ConditionTrue}
	readyFalse := metav1.Condition{Type: clusterv1.MachineReadyV1Beta2Condition, Status: metav1.ConditionFalse}
	readyUnknown := metav1.Condition{Type: clusterv1.MachineReadyV1Beta2Condition, Status: metav1.ConditionUnknown}

	availableTrue := metav1.Condition{Type: clusterv1.MachineAvailableV1Beta2Condition, Status: metav1.ConditionTrue}
	availableFalse := metav1.Condition{Type: clusterv1.MachineAvailableV1Beta2Condition, Status: metav1.ConditionFalse}
	availableUnknown := metav1.Condition{Type: clusterv1.MachineAvailableV1Beta2Condition, Status: metav1.ConditionUnknown}

	upToDateTrue := metav1.Condition{Type: clusterv1.MachineUpToDateV1Beta2Condition, Status: metav1.ConditionTrue}
	upToDateFalse := metav1.Condition{Type: clusterv1.MachineUpToDateV1Beta2Condition, Status: metav1.ConditionFalse}
	upToDateUnknown := metav1.Condition{Type: clusterv1.MachineUpToDateV1Beta2Condition, Status: metav1.ConditionUnknown}

	kcp := &controlplanev1.KubeadmControlPlane{}
	c := &internal.ControlPlane{
		KCP: kcp,
		Machines: collections.FromMachines(
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyTrue, availableTrue, upToDateTrue}}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyTrue, availableTrue, upToDateTrue}}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyFalse, availableFalse, upToDateTrue}}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyTrue, availableFalse, upToDateTrue}}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyFalse, availableFalse, upToDateFalse}}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m6"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyUnknown, availableUnknown, upToDateUnknown}}}},
		),
	}

	setReplicas(ctx, c.KCP, c.Machines)

	g.Expect(kcp.Status.V1Beta2).ToNot(BeNil())
	g.Expect(kcp.Status.V1Beta2.ReadyReplicas).ToNot(BeNil())
	g.Expect(*kcp.Status.V1Beta2.ReadyReplicas).To(Equal(int32(3)))
	g.Expect(kcp.Status.V1Beta2.AvailableReplicas).ToNot(BeNil())
	g.Expect(*kcp.Status.V1Beta2.AvailableReplicas).To(Equal(int32(2)))
	g.Expect(kcp.Status.V1Beta2.UpToDateReplicas).ToNot(BeNil())
	g.Expect(*kcp.Status.V1Beta2.UpToDateReplicas).To(Equal(int32(4)))
}

func Test_setInitializedCondition(t *testing.T) {
	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "KCP not initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneInitializedV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotInitializedV1Beta2Reason,
			},
		},
		{
			name: "KCP initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{Initialized: true},
				},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneInitializedV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneInitializedV1Beta2Reason,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setInitializedCondition(ctx, tt.controlPlane.KCP)

			condition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneInitializedV1Beta2Condition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(v1beta2conditions.MatchCondition(tt.expectCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setScalingUpCondition(t *testing.T) {
	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Replica not set",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status: metav1.ConditionUnknown,
				Reason: controlplanev1.KubeadmControlPlaneScalingUpWaitingForReplicasSetV1Beta2Reason,
			},
		},
		{
			name: "Not scaling up",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotScalingUpV1Beta2Reason,
			},
		},
		{
			name: "Not scaling up, infra template not found",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3)), MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{InfrastructureRef: corev1.ObjectReference{Kind: "AWSTemplate"}}},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				InfraMachineTemplateIsNotFound: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotScalingUpV1Beta2Reason,
				Message: "Scaling up would be blocked because AWSTemplate does not exist",
			},
		},
		{
			name: "Scaling up",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Reason,
				Message: "Scaling up from 3 to 5 replicas",
			},
		},
		{
			name: "Scaling up is always false when kcp is deleted",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now()})},
					Spec:       controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5))},
					Status:     controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotScalingUpV1Beta2Reason,
			},
		},
		{
			name: "Scaling up, infra template not found",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5)), MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{InfrastructureRef: corev1.ObjectReference{Kind: "AWSTemplate"}}},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				InfraMachineTemplateIsNotFound: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Reason,
				Message: "Scaling up from 3 to 5 replicas is blocked because AWSTemplate does not exist",
			},
		},
		{
			name: "Scaling up, preflight checks blocking",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				PreflightCheckResults: internal.PreflightCheckResults{
					HasDeletingMachine:               true,
					ControlPlaneComponentsNotHealthy: true,
					EtcdClusterNotHealthy:            true,
				},
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Reason,
				Message: "Scaling up from 3 to 5 replicas; waiting for Machine being deleted; waiting for control plane components to be healthy; waiting for etcd cluster to be healthy",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setScalingUpCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.Machines, tt.controlPlane.InfraMachineTemplateIsNotFound, tt.controlPlane.PreflightCheckResults)

			condition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneScalingUpV1Beta2Condition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(v1beta2conditions.MatchCondition(tt.expectCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setScalingDownCondition(t *testing.T) {
	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Replica not set",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status: metav1.ConditionUnknown,
				Reason: controlplanev1.KubeadmControlPlaneScalingDownWaitingForReplicasSetV1Beta2Reason,
			},
		},
		{
			name: "Not scaling down",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotScalingDownV1Beta2Reason,
			},
		},
		{
			name: "Scaling down",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 5},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Reason,
				Message: "Scaling down from 5 to 3 replicas",
			},
		},
		{
			name: "Scaling down to zero when kcp is deleted",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now()})},
					Spec:       controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status:     controlplanev1.KubeadmControlPlaneStatus{Replicas: 5},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Reason,
				Message: "Scaling down from 5 to 0 replicas",
			},
		},
		{
			name: "Scaling down with one stale machine",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(1))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1", DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Reason,
				Message: "Scaling down from 3 to 1 replicas; Machine m1 is in deletion since more than 30m",
			},
		},
		{
			name: "Scaling down with two stale machine",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(1))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1", DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2", DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Reason,
				Message: "Scaling down from 3 to 1 replicas; Machines m1, m2 are in deletion since more than 30m",
			},
		},
		{
			name: "Scaling down, preflight checks blocking",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(1))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: 3},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				PreflightCheckResults: internal.PreflightCheckResults{
					HasDeletingMachine:               true,
					ControlPlaneComponentsNotHealthy: true,
					EtcdClusterNotHealthy:            true,
				},
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Reason,
				Message: "Scaling down from 3 to 1 replicas; waiting for Machine being deleted; waiting for control plane components to be healthy; waiting for etcd cluster to be healthy",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setScalingDownCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.Machines, tt.controlPlane.PreflightCheckResults)

			condition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneScalingDownV1Beta2Condition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(v1beta2conditions.MatchCondition(tt.expectCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setMachinesReadyAndMachinesUpToDateConditions(t *testing.T) {
	readyTrue := metav1.Condition{Type: clusterv1.MachineReadyV1Beta2Condition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineReadyV1Beta2Reason}
	readyFalse := metav1.Condition{Type: clusterv1.MachineReadyV1Beta2Condition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotReadyV1Beta2Reason, Message: "NotReady"}

	upToDateTrue := metav1.Condition{Type: clusterv1.MachineUpToDateV1Beta2Condition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineUpToDateV1Beta2Reason}
	upToDateFalse := metav1.Condition{Type: clusterv1.MachineUpToDateV1Beta2Condition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotUpToDateV1Beta2Reason, Message: "NotUpToDate"}

	tests := []struct {
		name                            string
		controlPlane                    *internal.ControlPlane
		expectMachinesReadyCondition    metav1.Condition
		expectMachinesUpToDateCondition metav1.Condition
	}{
		{
			name: "Without machines",
			controlPlane: &internal.ControlPlane{
				KCP:      &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(),
			},
			expectMachinesReadyCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneMachinesReadyV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneMachinesReadyNoReplicasV1Beta2Reason,
			},
			expectMachinesUpToDateCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneMachinesUpToDateV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneMachinesUpToDateNoReplicasV1Beta2Reason,
			},
		},
		{
			name: "With machines",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyTrue, upToDateTrue}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyTrue, upToDateFalse}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{readyFalse, upToDateFalse}}}},
				),
			},
			expectMachinesReadyCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneMachinesReadyV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneMachinesNotReadyV1Beta2Reason,
				Message: "* Machine m3: NotReady",
			},
			expectMachinesUpToDateCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneMachinesUpToDateV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneMachinesNotUpToDateV1Beta2Reason,
				Message: "* Machines m2, m3: NotUpToDate",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setMachinesReadyCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.Machines)
			setMachinesUpToDateCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.Machines)

			readyCondition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneMachinesReadyV1Beta2Condition)
			g.Expect(readyCondition).ToNot(BeNil())
			g.Expect(*readyCondition).To(v1beta2conditions.MatchCondition(tt.expectMachinesReadyCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))

			upToDateCondition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneMachinesUpToDateV1Beta2Condition)
			g.Expect(upToDateCondition).ToNot(BeNil())
			g.Expect(*upToDateCondition).To(v1beta2conditions.MatchCondition(tt.expectMachinesUpToDateCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setRemediatingCondition(t *testing.T) {
	healthCheckSucceeded := clusterv1.Condition{Type: clusterv1.MachineHealthCheckSucceededV1Beta2Condition, Status: corev1.ConditionTrue}
	healthCheckNotSucceeded := clusterv1.Condition{Type: clusterv1.MachineHealthCheckSucceededV1Beta2Condition, Status: corev1.ConditionFalse}
	ownerRemediated := clusterv1.Condition{Type: clusterv1.MachineOwnerRemediatedCondition, Status: corev1.ConditionFalse}
	ownerRemediatedV1Beta2 := metav1.Condition{Type: clusterv1.MachineOwnerRemediatedV1Beta2Condition, Status: metav1.ConditionFalse, Reason: controlplanev1.KubeadmControlPlaneMachineRemediationMachineDeletingV1Beta2Reason, Message: "Machine is deleting"}

	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Without unhealthy machines",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneRemediatingV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotRemediatingV1Beta2Reason,
			},
		},
		{
			name: "With machines to be remediated by KCP",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckSucceeded}}},    // Healthy machine
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckNotSucceeded, ownerRemediated}, V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{ownerRemediatedV1Beta2}}}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneRemediatingV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneRemediatingV1Beta2Reason,
				Message: "* Machine m3: Machine is deleting",
			},
		},
		{
			name: "With one unhealthy machine not to be remediated by KCP",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckSucceeded}}},    // Healthy machine
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckSucceeded}}},    // Healthy machine
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneRemediatingV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotRemediatingV1Beta2Reason,
				Message: "Machine m2 is not healthy (not to be remediated by KubeadmControlPlane)",
			},
		},
		{
			name: "With two unhealthy machine not to be remediated by KCP",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: clusterv1.Conditions{healthCheckSucceeded}}},    // Healthy machine
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneRemediatingV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotRemediatingV1Beta2Reason,
				Message: "Machines m1, m2 are not healthy (not to be remediated by KubeadmControlPlane)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setRemediatingCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.MachinesToBeRemediatedByKCP(), tt.controlPlane.UnhealthyMachines())

			condition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneRemediatingV1Beta2Condition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(v1beta2conditions.MatchCondition(tt.expectCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func TestDeletingCondition(t *testing.T) {
	testCases := []struct {
		name            string
		kcp             *controlplanev1.KubeadmControlPlane
		deletingReason  string
		deletingMessage string
		expectCondition metav1.Condition
	}{
		{
			name: "deletionTimestamp not set",
			kcp: &controlplanev1.KubeadmControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kcp-test",
					Namespace: metav1.NamespaceDefault,
				},
			},
			deletingReason:  "",
			deletingMessage: "",
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneDeletingV1Beta2Condition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotDeletingV1Beta2Reason,
			},
		},
		{
			name: "deletionTimestamp set (waiting for control plane Machine deletion)",
			kcp: &controlplanev1.KubeadmControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "kcp-test",
					Namespace:         metav1.NamespaceDefault,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
			},
			deletingReason:  controlplanev1.KubeadmControlPlaneDeletingWaitingForMachineDeletionV1Beta2Reason,
			deletingMessage: "Deleting 3 Machines",
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneDeletingV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneDeletingWaitingForMachineDeletionV1Beta2Reason,
				Message: "Deleting 3 Machines",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			setDeletingCondition(ctx, tc.kcp, tc.deletingReason, tc.deletingMessage)

			deletingCondition := v1beta2conditions.Get(tc.kcp, controlplanev1.KubeadmControlPlaneDeletingV1Beta2Condition)
			g.Expect(deletingCondition).ToNot(BeNil())
			g.Expect(*deletingCondition).To(v1beta2conditions.MatchCondition(tc.expectCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_shouldSurfaceWhenAvailableTrue(t *testing.T) {
	reconcileTime := time.Now()

	apiServerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy11s := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-11 * time.Second)}}

	etcdMemberHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdMemberNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyV1Beta2Condition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}

	testCases := []struct {
		name    string
		machine *clusterv1.Machine
		want    bool
	}{
		{
			name:    "Machine doesn't have issues, it should not surface",
			machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, etcdMemberHealthy}}}},
			want:    false,
		},
		{
			name:    "Machine has issue set by less than 10s it should not surface",
			machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, etcdMemberNotHealthy}}}},
			want:    false,
		},
		{
			name:    "Machine has at least one issue set by more than 10s it should surface",
			machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy11s, etcdMemberNotHealthy}}}},
			want:    true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			got := shouldSurfaceWhenAvailableTrue(tc.machine, controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyV1Beta2Condition)
			g.Expect(got).To(Equal(tc.want))
		})
	}
}

func Test_setAvailableCondition(t *testing.T) {
	reconcileTime := time.Now()

	certificatesReady := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneCertificatesAvailableV1Beta2Condition, Status: metav1.ConditionTrue}
	certificatesNotReady := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneCertificatesAvailableV1Beta2Condition, Status: metav1.ConditionFalse}

	apiServerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy11s := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyV1Beta2Condition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-11 * time.Second)}}
	controllerManagerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineControllerManagerPodHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	schedulerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineSchedulerPodHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdPodHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}

	etcdMemberHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyV1Beta2Condition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdMemberNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyV1Beta2Condition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}

	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Kcp not yet initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: &bootstrapv1.LocalEtcd{}},
							},
						},
					},
				},
				EtcdMembers:                  []*etcd.Member{},
				EtcdMembersAgreeOnMemberList: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "Control plane not yet initialized",
			},
		},
		{
			name: "Failed to get etcd members right after being initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: &bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{
								{Type: controlplanev1.KubeadmControlPlaneInitializedV1Beta2Condition, Status: metav1.ConditionTrue, Reason: controlplanev1.KubeadmControlPlaneInitializedV1Beta2Reason, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-5 * time.Second)}},
							},
						},
					},
				},
				EtcdMembers: nil,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "Waiting for etcd to report the list of members",
			},
		},
		{
			name: "Failed to get etcd members, 2m after the cluster was initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: &bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{
								{Type: controlplanev1.KubeadmControlPlaneInitializedV1Beta2Condition, Status: metav1.ConditionTrue, Reason: controlplanev1.KubeadmControlPlaneInitializedV1Beta2Reason, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-5 * time.Minute)}},
							},
						},
					},
				},
				EtcdMembers: nil,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionUnknown,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableInspectionFailedV1Beta2Reason,
				Message: "Failed to get etcd members",
			},
		},
		{
			name: "Etcd members do not agree on member list",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: &bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{Initialized: true},
				},
				EtcdMembers:                  []*etcd.Member{},
				EtcdMembersAgreeOnMemberList: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "At least one etcd member reports a list of etcd members different than the list reported by other members",
			},
		},
		{
			name: "Etcd members do not agree on cluster ID",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: &bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{Initialized: true},
				},
				EtcdMembers:                  []*etcd.Member{},
				EtcdMembersAgreeOnMemberList: true,
				EtcdMembersAgreeOnClusterID:  false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "At least one etcd member reports a cluster ID different than the cluster ID reported by other members",
			},
		},
		{
			name: "Etcd members and machines list do not match",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: &bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{Initialized: true},
				},
				EtcdMembers:                       []*etcd.Member{},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "The list of etcd members does not match the list of Machines and Nodes",
			},
		},
		{
			name: "KCP is available",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableV1Beta2Reason,
			},
		},
		{
			name: "KCP is available, some control plane failures to be reported",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy11s, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableV1Beta2Reason,
				Message: "* 2 of 3 Machines have healthy control plane components, at least 1 required", // two are not healthy, but one just flipped recently and 10s safeguard against flake did not expired yet
			},
		},
		{
			name: "One not healthy etcd members, but within quorum",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{{}, {}, {}},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableV1Beta2Reason,
			},
		},
		{
			name: "Two not healthy k8s control plane, but one working",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{{}, {}, {}},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableV1Beta2Reason,
			},
		},
		{
			name: "KCP is deleting",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: ptr.To(metav1.Now()),
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "* Control plane metadata.deletionTimestamp is set",
			},
		},
		{
			name: "Certificates are not available",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesNotReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "* Control plane certificates are not available",
			},
		},
		{
			name: "Not enough healthy etcd members",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{{}, {}, {}},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "* 1 of 3 etcd members is healthy, at least 2 required for etcd quorum",
			},
		},
		{
			name: "Not enough healthy K8s control planes",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}}}},
				),
				EtcdMembers:                       []*etcd.Member{{}, {}, {}},
				EtcdMembersAgreeOnMemberList:      true,
				EtcdMembersAgreeOnClusterID:       true,
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "* There are no Machines with healthy control plane components, at least 1 required",
			},
		},
		{
			name: "External etcd, at least one K8s control plane",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{External: &bootstrapv1.ExternalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
				),
				EtcdMembers:                       nil,
				EtcdMembersAgreeOnMemberList:      false,
				EtcdMembersAgreeOnClusterID:       false,
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableV1Beta2Reason,
			},
		},
		{
			name: "External etcd, at least one K8s control plane, some control plane failures to be reported",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{External: &bootstrapv1.ExternalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy11s, controllerManagerPodHealthy, schedulerPodHealthy}}}},
				),
				EtcdMembers:                       nil,
				EtcdMembersAgreeOnMemberList:      false,
				EtcdMembersAgreeOnClusterID:       false,
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableV1Beta2Reason,
				Message: "* 2 of 3 Machines have healthy control plane components, at least 1 required", // two are not healthy, but one just flipped recently and 10s safeguard against flake did not expired yet
			},
		},
		{
			name: "External etcd, not enough healthy K8s control planes",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: &bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{External: &bootstrapv1.ExternalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialized: true,
						V1Beta2: &controlplanev1.KubeadmControlPlaneV1Beta2Status{
							Conditions: []metav1.Condition{certificatesReady},
						},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{V1Beta2: &clusterv1.MachineV1Beta2Status{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}}}},
				),
				EtcdMembers:                       nil,
				EtcdMembersAgreeOnMemberList:      false,
				EtcdMembersAgreeOnClusterID:       false,
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableV1Beta2Reason,
				Message: "* There are no Machines with healthy control plane components, at least 1 required",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setAvailableCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.IsEtcdManaged(), tt.controlPlane.EtcdMembers, tt.controlPlane.EtcdMembersAgreeOnMemberList, tt.controlPlane.EtcdMembersAgreeOnClusterID, tt.controlPlane.EtcdMembersAndMachinesAreMatching, tt.controlPlane.Machines)

			availableCondition := v1beta2conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneAvailableV1Beta2Condition)
			g.Expect(availableCondition).ToNot(BeNil())
			g.Expect(*availableCondition).To(v1beta2conditions.MatchCondition(tt.expectCondition, v1beta2conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func TestKubeadmControlPlaneReconciler_updateStatusNoMachines(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: "test/v1alpha1",
					Kind:       "UnknownInfraMachine",
					Name:       "foo",
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())

	fakeClient := newFakeClient(kcp.DeepCopy(), cluster.DeepCopy())

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: map[string]*clusterv1.Machine{},
			Workload: &fakeWorkloadCluster{},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:     kcp,
		Cluster: cluster,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateStatus(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Replicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.ReadyReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.UnavailableReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Initialized).To(BeFalse())
	g.Expect(kcp.Status.Ready).To(BeFalse())
	g.Expect(kcp.Status.Selector).NotTo(BeEmpty())
	g.Expect(kcp.Status.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.FailureReason).To(BeEquivalentTo(""))
}

func TestKubeadmControlPlaneReconciler_updateStatusAllMachinesNotReady(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: "test/v1alpha1",
					Kind:       "UnknownInfraMachine",
					Name:       "foo",
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())

	machines := map[string]*clusterv1.Machine{}
	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy()}
	for i := range 3 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, false)
		objs = append(objs, n, m)
		machines[m.Name] = m
	}

	fakeClient := newFakeClient(objs...)

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateStatus(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Replicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.ReadyReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.UnavailableReplicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.Selector).NotTo(BeEmpty())
	g.Expect(kcp.Status.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.FailureReason).To(BeEquivalentTo(""))
	g.Expect(kcp.Status.Initialized).To(BeFalse())
	g.Expect(kcp.Status.Ready).To(BeFalse())
}

func TestKubeadmControlPlaneReconciler_updateStatusAllMachinesReady(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "foo",
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: "test/v1alpha1",
					Kind:       "UnknownInfraMachine",
					Name:       "foo",
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())

	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy(), kubeadmConfigMap()}
	machines := map[string]*clusterv1.Machine{}
	for i := range 3 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, true)
		objs = append(objs, n, m)
		machines[m.Name] = m
	}

	fakeClient := newFakeClient(objs...)

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					Nodes:            3,
					ReadyNodes:       3,
					HasKubeadmConfig: true,
				},
			},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateStatus(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Replicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.ReadyReplicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.UnavailableReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Selector).NotTo(BeEmpty())
	g.Expect(kcp.Status.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.FailureReason).To(BeEquivalentTo(""))
	g.Expect(kcp.Status.Initialized).To(BeTrue())
	g.Expect(conditions.IsTrue(kcp, controlplanev1.AvailableCondition)).To(BeTrue())
	g.Expect(conditions.IsTrue(kcp, controlplanev1.MachinesCreatedCondition)).To(BeTrue())
	g.Expect(kcp.Status.Ready).To(BeTrue())
}

func TestKubeadmControlPlaneReconciler_updateStatusMachinesReadyMixed(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: "test/v1alpha1",
					Kind:       "UnknownInfraMachine",
					Name:       "foo",
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())
	machines := map[string]*clusterv1.Machine{}
	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy()}
	for i := range 4 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, false)
		machines[m.Name] = m
		objs = append(objs, n, m)
	}
	m, n := createMachineNodePair("testReady", cluster, kcp, true)
	objs = append(objs, n, m, kubeadmConfigMap())
	machines[m.Name] = m
	fakeClient := newFakeClient(objs...)

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					Nodes:            5,
					ReadyNodes:       1,
					HasKubeadmConfig: true,
				},
			},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateStatus(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Replicas).To(BeEquivalentTo(5))
	g.Expect(kcp.Status.ReadyReplicas).To(BeEquivalentTo(1))
	g.Expect(kcp.Status.UnavailableReplicas).To(BeEquivalentTo(4))
	g.Expect(kcp.Status.Selector).NotTo(BeEmpty())
	g.Expect(kcp.Status.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.FailureReason).To(BeEquivalentTo(""))
	g.Expect(kcp.Status.Initialized).To(BeTrue())
	g.Expect(kcp.Status.Ready).To(BeTrue())
}

func TestKubeadmControlPlaneReconciler_machinesCreatedIsIsTrueEvenWhenTheNodesAreNotReady(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version:  "v1.16.6",
			Replicas: ptr.To[int32](3),
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				InfrastructureRef: corev1.ObjectReference{
					APIVersion: "test/v1alpha1",
					Kind:       "UnknownInfraMachine",
					Name:       "foo",
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())
	machines := map[string]*clusterv1.Machine{}
	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy()}
	// Create the desired number of machines
	for i := range 3 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, false)
		machines[m.Name] = m
		objs = append(objs, n, m)
	}

	fakeClient := newFakeClient(objs...)

	// Set all the machines to `not ready`
	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					Nodes:            0,
					ReadyNodes:       0,
					HasKubeadmConfig: true,
				},
			},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateStatus(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Replicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.ReadyReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.UnavailableReplicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.Ready).To(BeFalse())
	g.Expect(conditions.IsTrue(kcp, controlplanev1.MachinesCreatedCondition)).To(BeTrue())
}

func kubeadmConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubeadm-config",
			Namespace: metav1.NamespaceSystem,
		},
	}
}
