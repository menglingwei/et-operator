package controllers

import (
	"bytes"
	"fmt"
	kaiv1alpha1 "github.com/AliyunContainerService/et-operator/api/v1alpha1"
	"github.com/AliyunContainerService/et-operator/pkg/util"
	logger "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"path"
	"strings"
)

func newLauncher(obj interface{}) *corev1.Pod {
	job, _ := obj.(*kaiv1alpha1.TrainingJob)
	launcherName := job.Name + launcherSuffix
	labels := GenLabels(job.Name)
	labels[labelTrainingRoleType] = launcher
	podSpec := job.Spec.ETReplicaSpecs.Launcher.Template.DeepCopy()
	// copy the labels and annotations to pod from PodTemplate
	if len(podSpec.Labels) == 0 {
		podSpec.Labels = make(map[string]string)
	}
	for key, value := range labels {
		podSpec.Labels[key] = value
	}
	podSpec.Spec.InitContainers = append(podSpec.Spec.InitContainers, initContainer(job))
	//podSpec.Spec.InitContainers = append(podSpec.Spec.InitContainers, kubedeliveryContainer())
	if len(podSpec.Spec.Containers) == 0 {
		logger.Errorln("Launcher pod does not have any containers in its spec")
		return nil
	}

	container := podSpec.Spec.Containers[0]
	container.VolumeMounts = append(container.VolumeMounts,
		corev1.VolumeMount{
			Name:      hostfileVolumeName,
			MountPath: hostfileMountPath,
		},
		corev1.VolumeMount{
			Name:      configVolumeName,
			MountPath: configMountPath,
		},
		corev1.VolumeMount{
			Name:      kubectlVolumeName,
			MountPath: kubectlMountPath,
		})
	//container.Env = append(container.Env, corev1.EnvVar{
	//	Name:  "OMPI_MCA_plm_rsh_agent",
	//	Value: getKubexecPath(),
	//})
	podSpec.Spec.Containers[0] = container
	podSpec.Spec.ServiceAccountName = launcherName

	setRestartPolicy(podSpec)
	hostfileMode := int32(0444)
	scriptMode := int32(0555)

	podSpec.Spec.Volumes = append(podSpec.Spec.Volumes,
		corev1.Volume{
			Name: hostfileVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: kubectlVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: configVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: job.Name + configSuffix,
					},
					Items: []corev1.KeyToPath{
						{
							Key:  hostfileName,
							Path: hostfileName,
							Mode: &hostfileMode,
						},
						{
							Key:  discoverHostName,
							Path: discoverHostName,
							Mode: &hostfileMode,
						},
						{
							Key:  kubexeclFileName,
							Path: kubexeclFileName,
							Mode: &scriptMode,
						},
					},
				},
			},
		})
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        launcherName,
			Namespace:   job.Namespace,
			Labels:      podSpec.Labels,
			Annotations: podSpec.Annotations,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Spec: podSpec.Spec,
	}
}

func kubedeliveryContainer() corev1.Container {
	return corev1.Container{
		Name:            "kubectl-delivery",
		Image:           "registry.cn-zhangjiakou.aliyuncs.com/kube-ai/kubectl-delivery:latest",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{
				Name:  "TARGET_DIR",
				Value: kubectlMountPath,
			},
			{
				Name:  "NAMESPACE",
				Value: "default",
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      kubectlVolumeName,
				MountPath: kubectlMountPath,
			},
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(initContainerCpu),
				corev1.ResourceMemory:           resource.MustParse(initContainerMem),
				corev1.ResourceEphemeralStorage: resource.MustParse(initContainerEphStorage),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(initContainerCpu),
				corev1.ResourceMemory:           resource.MustParse(initContainerMem),
				corev1.ResourceEphemeralStorage: resource.MustParse(initContainerEphStorage),
			},
		},
	}
}
func initContainer(job *kaiv1alpha1.TrainingJob) corev1.Container {
	originHostfilePath := path.Join(configMountPath, hostfileName)
	mountHostfilePath := getHostfilePath(job)
	cpHostfile := fmt.Sprintf("cp %s %s && chmod 600 %s",
		originHostfilePath,
		mountHostfilePath,
		mountHostfilePath)
	originDiscoverHostPath := path.Join(configMountPath, discoverHostName)
	discoverHostPath := path.Join(hostfileMountPath, discoverHostName)
	cpDiscoverHostfile := fmt.Sprintf("cp %s %s && chmod +x %s",
		originDiscoverHostPath,
		discoverHostPath,
		discoverHostPath)
	return corev1.Container{
		Name:            initContainerName,
		Image:           initContainerImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      hostfileVolumeName,
				MountPath: hostfileMountPath,
			},
			{
				Name:      configVolumeName,
				MountPath: configMountPath,
			},
		},
		Command: []string{
			"sh",
			"-c",
			strings.Join([]string{cpHostfile, cpDiscoverHostfile}, " && "),
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(initContainerCpu),
				corev1.ResourceMemory:           resource.MustParse(initContainerMem),
				corev1.ResourceEphemeralStorage: resource.MustParse(initContainerEphStorage),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse(initContainerCpu),
				corev1.ResourceMemory:           resource.MustParse(initContainerMem),
				corev1.ResourceEphemeralStorage: resource.MustParse(initContainerEphStorage),
			},
		},
	}
}

func newWorker(obj interface{}, name string, index string) *corev1.Pod {
	job, _ := obj.(*kaiv1alpha1.TrainingJob)
	labels := GenLabels(job.Name)
	labels[labelTrainingRoleType] = worker
	labels[replicaIndexLabel] = index
	podSpec := job.Spec.ETReplicaSpecs.Worker.Template.DeepCopy()

	// keep the labels which are set in PodTemplate
	if len(podSpec.Labels) == 0 {
		podSpec.Labels = make(map[string]string)
	}
	for key, value := range labels {
		podSpec.Labels[key] = value
	}

	// RestartPolicy=Never
	setRestartPolicy(podSpec)

	if len(podSpec.Spec.Containers) == 0 {
		logger.Errorln("Worker pod does not have any containers in its spec")
		return nil
	}
	container := podSpec.Spec.Containers[0]

	// if we want to use ssh, will start sshd service firstly.
	if len(container.Command) == 0 {
		//container.Command = []string{"sh", "-c", "sleep 365d"}
		container.Command = []string{"sh", "-c", "/usr/sbin/sshd  && sleep 365d"}
	}
	podSpec.Spec.Containers[0] = container

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   job.Namespace,
			Labels:      podSpec.Labels,
			Annotations: podSpec.Annotations,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Spec: podSpec.Spec,
	}
}

func newLauncherRoleBinding(obj interface{}) *rbacv1.RoleBinding {
	job, _ := obj.(*kaiv1alpha1.TrainingJob)
	launcherName := job.Name + launcherSuffix
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      launcherName,
			Namespace: job.Namespace,
			Labels: map[string]string{
				"app": job.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      launcherName,
				Namespace: job.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     launcherName,
		},
	}
}

func newService(obj interface{}, name string, index string) *corev1.Service {
	job, _ := obj.(*kaiv1alpha1.TrainingJob)
	labels := GenLabels(job.Name)
	labels[labelTrainingRoleType] = worker
	labels[replicaIndexLabel] = index
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: job.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  labels,
			Ports: []corev1.ServicePort{
				{
					Name: "ssh-port",
					Port: 22,
				},
			},
		},
	}
}

func newLauncherRole(obj interface{}, workerReplicas int32) *rbacv1.Role {
	job, _ := obj.(*kaiv1alpha1.TrainingJob)
	//var podNames []string
	//for i := 0; i < int(workerReplicas); i++ {
	//	podNames = append(podNames, fmt.Sprintf("%s%s-%d", job.Name, workerSuffix, i))
	//}
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name + launcherSuffix,
			Namespace: job.Namespace,
			Labels: map[string]string{
				"app": job.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "list", "watch"},
				APIGroups: []string{""},
				Resources: []string{"pods"},
			},
			{
				Verbs:     []string{"create"},
				APIGroups: []string{""},
				Resources: []string{"pods/exec"},
				//ResourceNames: podNames,
			},
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"configmap"},
				ResourceNames: []string{job.Name + configSuffix},
			},
		},
	}
}

func newSecret(job *kaiv1alpha1.TrainingJob) *corev1.Secret {
	data, _ := util.GenerateRsaKey()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name,
			Namespace: job.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Data: data,
	}
}

func newLauncherServiceAccount(obj interface{}) *corev1.ServiceAccount {
	job, _ := obj.(*kaiv1alpha1.TrainingJob)
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name + launcherSuffix,
			Namespace: job.Namespace,
			Labels: map[string]string{
				"app": job.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
	}
}

func getSlots(job *kaiv1alpha1.TrainingJob) int {
	if job.Spec.SlotsPerWorker != nil {
		return int(*job.Spec.SlotsPerWorker)
	}
	if job.Spec.ETReplicaSpecs.Worker != nil {
		container := job.Spec.ETReplicaSpecs.Worker.Template.Spec.Containers[0]
		if container.Resources.Limits != nil {
			if val, ok := job.Spec.ETReplicaSpecs.Worker.Template.Spec.Containers[0].Resources.Limits[gpuResourceName]; ok {
				processingUnits, _ := val.AsInt64()
				return int(processingUnits)
			}
		}
	}
	return 1
}

func newHostfileConfigMap(job *kaiv1alpha1.TrainingJob) *corev1.ConfigMap {
	kubExecCmd := fmt.Sprintf(`#!/bin/sh
set -x
POD_NAME=$1
shift
%s/kubectl exec ${POD_NAME}`, kubectlMountPath)

	containers := job.Spec.ETReplicaSpecs.Worker.Template.Spec.Containers
	if len(containers) > 0 {
		kubExecCmd = fmt.Sprintf("%s --container %s", kubExecCmd, containers[0].Name)
	}
	kubExecCmd = fmt.Sprintf("%s -- /bin/sh -c \"$*\"", kubExecCmd)

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name + configSuffix,
			Namespace: job.Namespace,
			Labels: map[string]string{
				"app": job.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(job, kaiv1alpha1.SchemeGroupVersionKind),
			},
		},
		Data: map[string]string{
			kubexeclFileName: kubExecCmd,
			hostfileName:     getHostfileContent(job.Status.CurrentWorkers, getSlots(job)),
			discoverHostName: getDiscoverHostContent(job),
		},
	}
}

func getHostfileContent(workers []string, slot int) string {
	var buffer bytes.Buffer
	for _, worker := range workers {
		buffer.WriteString(fmt.Sprintf("%s:%d\n", worker, slot))
	}
	return buffer.String()
}

func getDiscoverHostContent(job *kaiv1alpha1.TrainingJob) string {
	return fmt.Sprintf(`#!/bin/bash
while read line
do
echo $line
done < %s
`, getHostfilePath(job))
}

func getHostfilePath(_ *kaiv1alpha1.TrainingJob) string {
	return path.Join(hostfileMountPath, hostfileName)
}

func getKubexecPath() string {
	return path.Join(configMountPath, kubexeclFileName)
}
