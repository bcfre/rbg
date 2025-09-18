package reconciler

import (
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_objectMetaEqual(t *testing.T) {
	type args struct {
		meta1 v1.ObjectMeta
		meta2 v1.ObjectMeta
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "test system labels",
			args: args{
				meta1: v1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/component":            "lws",
						"app.kubernetes.io/instance":             "restart-policy",
						"app.kubernetes.io/managed-by":           "rolebasedgroup-controller",
						"app.kubernetes.io/name":                 "restart-policy",
						"rolebasedgroup.workloads.x-k8s.io/name": "restart-policy",
						"rolebasedgroup.workloads.x-k8s.io/role": "lws",
					},
				},
				meta2: v1.ObjectMeta{
					Labels: map[string]string{
						"rolebasedgroup.workloads.x-k8s.io/name": "restart-policy",
						"rolebasedgroup.workloads.x-k8s.io/role": "lws",
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "test system annotations",
			args: args{
				meta1: v1.ObjectMeta{
					Annotations: map[string]string{
						"deployment.kubernetes.io/revision":           "1",
						"rolebasedgroup.workloads.x-k8s.io/role-size": "4",
					},
				},
				meta2: v1.ObjectMeta{
					Annotations: map[string]string{
						"rolebasedgroup.workloads.x-k8s.io/role-size": "4",
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "test system annotations",
			args: args{
				meta1: v1.ObjectMeta{
					Annotations: map[string]string{
						"rolebasedgroup.workloads.x-k8s.io/role-size": "4",
					},
				},
				meta2: v1.ObjectMeta{
					Annotations: nil,
				},
			},
			want:    true,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := objectMetaEqual(tt.args.meta1, tt.args.meta2)
			if (err != nil) != tt.wantErr {
				t.Errorf("objectMetaEqual() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("objectMetaEqual() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainerEqual(t *testing.T) {
	baseContainer := corev1.Container{
		Name:    "app",
		Image:   "nginx:latest",
		Command: []string{"/bin/sh"},
		Args:    []string{"-c", "echo hello"},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				"cpu":    resource.MustParse("100m"),
				"memory": resource.MustParse("100Mi"),
			},
		},
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{Name: "ENV", Value: "prod"},
		},
		StartupProbe:   &corev1.Probe{TimeoutSeconds: 10},
		LivenessProbe:  &corev1.Probe{TimeoutSeconds: 10},
		ReadinessProbe: &corev1.Probe{TimeoutSeconds: 10},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "config", MountPath: "/etc/config"},
		},
	}

	t.Run("equal containers", func(t *testing.T) {
		ok, err := containerEqual(baseContainer, baseContainer)
		if !ok || err != nil {
			t.Fatalf("expected equal, got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("different name", func(t *testing.T) {
		c2 := baseContainer
		c2.Name = "diff"
		ok, err := containerEqual(baseContainer, c2)
		if ok || err == nil || err.Error() != "container name not equal" {
			t.Fatalf("expected name not equal error, got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("different image", func(t *testing.T) {
		c2 := baseContainer
		c2.Image = "busybox"
		ok, err := containerEqual(baseContainer, c2)
		if ok || err == nil || err.Error() != "container image not equal" {
			t.Fatalf("expected image not equal error, got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("different env", func(t *testing.T) {
		c2 := baseContainer
		c2.Env = []corev1.EnvVar{
			{Name: "ENV", Value: "dev"},
		}
		ok, err := containerEqual(baseContainer, c2)
		if ok || err == nil || !contains(err.Error(), "env not equal") {
			t.Fatalf("expected env not equal error, got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("different startup probe", func(t *testing.T) {
		c2 := baseContainer
		c2.StartupProbe = &corev1.Probe{}
		ok, err := containerEqual(baseContainer, c2)
		if ok || err == nil || !contains(err.Error(), "container startup probe not equal") {
			t.Fatalf("expected startup probe not equal error, got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("different liveness probe", func(t *testing.T) {
		c2 := baseContainer
		c2.LivenessProbe = &corev1.Probe{TimeoutSeconds: 5}
		ok, err := containerEqual(baseContainer, c2)
		if ok || err == nil || !contains(err.Error(), "container liveness probe not equal") {
			t.Fatalf("expected liveness probe not equal error, got ok=%v, err=%v", ok, err)
		}
	})

	t.Run("different readiness probe", func(t *testing.T) {
		c2 := baseContainer
		c2.ReadinessProbe = &corev1.Probe{TimeoutSeconds: 5}
		ok, err := containerEqual(baseContainer, c2)
		if ok || err == nil || !contains(err.Error(), "container readiness probe not equal") {
			t.Fatalf("expected readiness probe not equal error, got ok=%v, err=%v", ok, err)
		}
	})
}

// contains checks if a substring exists in a string
func contains(s, sub string) bool {
	return reflect.ValueOf(s).String() != "" && (len(s) >= len(sub) && (func() bool {
		return fmt.Sprint(s)[0:len(sub)] == sub || contains(s[1:], sub)
	})())
}
