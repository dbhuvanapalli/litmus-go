package lib

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	clients "github.com/litmuschaos/litmus-go/pkg/clients"
	experimentTypes "github.com/litmuschaos/litmus-go/pkg/generic/stress-chaos/types"
	"github.com/litmuschaos/litmus-go/pkg/log"
	"github.com/litmuschaos/litmus-go/pkg/probe"
	"github.com/litmuschaos/litmus-go/pkg/status"
	"github.com/litmuschaos/litmus-go/pkg/types"
	"github.com/litmuschaos/litmus-go/pkg/utils/common"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//PrepareAndInjectStressChaos contains the prepration & injection steps for the stress experiments.
func PrepareAndInjectStressChaos(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails) error {

	var err error
	//Setup the tunables if provided in range
	SetChaosTunables(experimentsDetails)

	switch experimentsDetails.StressType {
	case "pod-cpu-stress":
		log.InfoWithValues("[Info]: The chaos tunables are:", logrus.Fields{
			"CPU Core":            experimentsDetails.CPUcores,
			"CPU Load Percentage": experimentsDetails.CPULoad,
			"Sequence":            experimentsDetails.Sequence,
			"PodsAffectedPerc":    experimentsDetails.PodsAffectedPerc,
		})

	case "pod-memory-stress":
		log.InfoWithValues("[Info]: The chaos tunables are:", logrus.Fields{
			"Number of Workers":  experimentsDetails.NumberOfWorkers,
			"Memory Consumption": experimentsDetails.MemoryConsumption,
			"Sequence":           experimentsDetails.Sequence,
			"PodsAffectedPerc":   experimentsDetails.PodsAffectedPerc,
		})

	case "pod-io-stress":
		log.InfoWithValues("[Info]: The chaos tunables are:", logrus.Fields{
			"FilesystemUtilizationPercentage": experimentsDetails.FilesystemUtilizationPercentage,
			"FilesystemUtilizationBytes":      experimentsDetails.FilesystemUtilizationBytes,
			"NumberOfWorkers":                 experimentsDetails.NumberOfWorkers,
			"Sequence":                        experimentsDetails.Sequence,
			"PodsAffectedPerc":                experimentsDetails.PodsAffectedPerc,
		})
	}

	// Get the target pod details for the chaos execution
	// if the target pod is not defined it will derive the random target pod list using pod affected percentage
	if experimentsDetails.TargetPods == "" && chaosDetails.AppDetail == nil {
		return errors.Errorf("Please provide one of the appLabel or TARGET_PODS")
	}
	targetPodList, err := common.GetTargetPods(experimentsDetails.NodeLabel, experimentsDetails.TargetPods, experimentsDetails.PodsAffectedPerc, clients, chaosDetails)
	if err != nil {
		return err
	}

	//Waiting for the ramp time before chaos injection
	if experimentsDetails.RampTime != 0 {
		log.Infof("[Ramp]: Waiting for the %vs ramp time before injecting chaos", experimentsDetails.RampTime)
		common.WaitForDuration(experimentsDetails.RampTime)
	}

	// Getting the serviceAccountName, need permission inside helper pod to create the events
	if experimentsDetails.ChaosServiceAccount == "" {
		experimentsDetails.ChaosServiceAccount, err = common.GetServiceAccount(experimentsDetails.ChaosNamespace, experimentsDetails.ChaosPodName, clients)
		if err != nil {
			return errors.Errorf("unable to get the serviceAccountName, err: %v", err)
		}
	}

	if experimentsDetails.EngineName != "" {
		if err := common.SetHelperData(chaosDetails, experimentsDetails.SetHelperData, clients); err != nil {
			return err
		}
	}

	experimentsDetails.IsTargetContainerProvided = (experimentsDetails.TargetContainer != "")
	switch strings.ToLower(experimentsDetails.Sequence) {
	case "serial":
		if err = injectChaosInSerialMode(experimentsDetails, targetPodList, clients, chaosDetails, resultDetails, eventsDetails); err != nil {
			return err
		}
	case "parallel":
		if err = injectChaosInParallelMode(experimentsDetails, targetPodList, clients, chaosDetails, resultDetails, eventsDetails); err != nil {
			return err
		}
	default:
		return errors.Errorf("%v sequence is not supported", experimentsDetails.Sequence)
	}

	return nil
}

// injectChaosInSerialMode inject the stress chaos in all target application serially (one by one)
func injectChaosInSerialMode(experimentsDetails *experimentTypes.ExperimentDetails, targetPodList apiv1.PodList, clients clients.ClientSets, chaosDetails *types.ChaosDetails, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails) error {

	var err error
	// run the probes during chaos
	if len(resultDetails.ProbeDetails) != 0 {
		if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
			return err
		}
	}

	// creating the helper pod to perform the stress chaos
	for _, pod := range targetPodList.Items {

		//Get the target container name of the application pod
		if !experimentsDetails.IsTargetContainerProvided {
			experimentsDetails.TargetContainer, err = common.GetTargetContainer(pod.Namespace, pod.Name, clients)
			if err != nil {
				return errors.Errorf("unable to get the target container name, err: %v", err)
			}
		}

		log.InfoWithValues("[Info]: Details of application under chaos injection", logrus.Fields{
			"PodName":       pod.Name,
			"NodeName":      pod.Spec.NodeName,
			"ContainerName": experimentsDetails.TargetContainer,
		})
		runID := common.GetRunID()
		if err := createHelperPod(experimentsDetails, clients, chaosDetails, fmt.Sprintf("%s:%s:%s", pod.Name, pod.Namespace, experimentsDetails.TargetContainer), pod.Spec.NodeName, runID); err != nil {
			return errors.Errorf("unable to create the helper pod, err: %v", err)
		}

		appLabel := fmt.Sprintf("app=%s-helper-%s", experimentsDetails.ExperimentName, runID)

		//checking the status of the helper pods, wait till the pod comes to running state else fail the experiment
		log.Info("[Status]: Checking the status of the helper pods")
		if err := status.CheckHelperStatus(experimentsDetails.ChaosNamespace, appLabel, experimentsDetails.Timeout, experimentsDetails.Delay, clients); err != nil {
			common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
			return errors.Errorf("helper pods are not in running state, err: %v", err)
		}

		// Wait till the completion of the helper pod
		// set an upper limit for the waiting time
		log.Info("[Wait]: waiting till the completion of the helper pod")
		podStatus, err := status.WaitForCompletion(experimentsDetails.ChaosNamespace, appLabel, clients, experimentsDetails.ChaosDuration+experimentsDetails.Timeout, experimentsDetails.ExperimentName)
		if err != nil || podStatus == "Failed" {
			common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
			return common.HelperFailedError(err)
		}

		//Deleting all the helper pod for stress chaos
		log.Info("[Cleanup]: Deleting the helper pod")
		err = common.DeleteAllPod(appLabel, experimentsDetails.ChaosNamespace, chaosDetails.Timeout, chaosDetails.Delay, clients)
		if err != nil {
			return errors.Errorf("unable to delete the helper pods, err: %v", err)
		}
	}

	return nil
}

// injectChaosInParallelMode inject the stress chaos in all target application in parallel mode (all at once)
func injectChaosInParallelMode(experimentsDetails *experimentTypes.ExperimentDetails, targetPodList apiv1.PodList, clients clients.ClientSets, chaosDetails *types.ChaosDetails, resultDetails *types.ResultDetails, eventsDetails *types.EventDetails) error {

	var err error
	// run the probes during chaos
	if len(resultDetails.ProbeDetails) != 0 {
		if err := probe.RunProbes(chaosDetails, clients, resultDetails, "DuringChaos", eventsDetails); err != nil {
			return err
		}
	}

	runID := common.GetRunID()
	targets := common.FilterPodsForNodes(targetPodList, experimentsDetails.TargetContainer)

	for node, tar := range targets {
		var targetsPerNode []string
		for _, k := range tar.Target {
			targetsPerNode = append(targetsPerNode, fmt.Sprintf("%s:%s:%s", k.Name, k.Namespace, k.TargetContainer))
		}

		if err := createHelperPod(experimentsDetails, clients, chaosDetails, strings.Join(targetsPerNode, ";"), node, runID); err != nil {
			return errors.Errorf("unable to create the helper pod, err: %v", err)
		}
	}

	appLabel := fmt.Sprintf("app=%s-helper-%s", experimentsDetails.ExperimentName, runID)

	//checking the status of the helper pods, wait till the pod comes to running state else fail the experiment
	log.Info("[Status]: Checking the status of the helper pods")
	if err := status.CheckHelperStatus(experimentsDetails.ChaosNamespace, appLabel, experimentsDetails.Timeout, experimentsDetails.Delay, clients); err != nil {
		common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
		return errors.Errorf("helper pods are not in running state, err: %v", err)
	}

	// Wait till the completion of the helper pod
	// set an upper limit for the waiting time
	log.Info("[Wait]: waiting till the completion of the helper pod")
	podStatus, err := status.WaitForCompletion(experimentsDetails.ChaosNamespace, appLabel, clients, experimentsDetails.ChaosDuration+experimentsDetails.Timeout, experimentsDetails.ExperimentName)
	if err != nil || podStatus == "Failed" {
		common.DeleteAllHelperPodBasedOnJobCleanupPolicy(appLabel, chaosDetails, clients)
		return common.HelperFailedError(err)
	}

	//Deleting all the helper pod for stress chaos
	log.Info("[Cleanup]: Deleting all the helper pod")
	err = common.DeleteAllPod(appLabel, experimentsDetails.ChaosNamespace, chaosDetails.Timeout, chaosDetails.Delay, clients)
	if err != nil {
		return errors.Errorf("unable to delete the helper pods, err: %v", err)
	}

	return nil
}

// createHelperPod derive the attributes for helper pod and create the helper pod
func createHelperPod(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, chaosDetails *types.ChaosDetails, targets, nodeName, runID string) error {

	privilegedEnable := true
	terminationGracePeriodSeconds := int64(experimentsDetails.TerminationGracePeriodSeconds)

	helperPod := &apiv1.Pod{
		ObjectMeta: v1.ObjectMeta{
			GenerateName: experimentsDetails.ExperimentName + "-helper-",
			Namespace:    experimentsDetails.ChaosNamespace,
			Labels:       common.GetHelperLabels(chaosDetails.Labels, runID, experimentsDetails.ExperimentName),
			Annotations:  chaosDetails.Annotations,
		},
		Spec: apiv1.PodSpec{
			HostPID:                       true,
			TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
			ImagePullSecrets:              chaosDetails.ImagePullSecrets,
			ServiceAccountName:            experimentsDetails.ChaosServiceAccount,
			RestartPolicy:                 apiv1.RestartPolicyNever,
			NodeName:                      nodeName,

			Volumes: []apiv1.Volume{
				{
					Name: "socket-path",
					VolumeSource: apiv1.VolumeSource{
						HostPath: &apiv1.HostPathVolumeSource{
							Path: experimentsDetails.SocketPath,
						},
					},
				},
				{
					Name: "sys-path",
					VolumeSource: apiv1.VolumeSource{
						HostPath: &apiv1.HostPathVolumeSource{
							Path: "/sys",
						},
					},
				},
			},

			Containers: []apiv1.Container{
				{
					Name:            experimentsDetails.ExperimentName,
					Image:           experimentsDetails.LIBImage,
					ImagePullPolicy: apiv1.PullPolicy(experimentsDetails.LIBImagePullPolicy),
					Command: []string{
						"/bin/bash",
					},
					Args: []string{
						"-c",
						"./helpers -name stress-chaos",
					},
					Resources: chaosDetails.Resources,
					Env:       getPodEnv(experimentsDetails, targets),
					VolumeMounts: []apiv1.VolumeMount{
						{
							Name:      "socket-path",
							MountPath: experimentsDetails.SocketPath,
						},
						{
							Name:      "sys-path",
							MountPath: "/sys",
						},
					},
					SecurityContext: &apiv1.SecurityContext{
						Privileged: &privilegedEnable,
						RunAsUser:  ptrint64(0),
						Capabilities: &apiv1.Capabilities{
							Add: []apiv1.Capability{
								"SYS_ADMIN",
							},
						},
					},
				},
			},
		},
	}

	_, err := clients.KubeClient.CoreV1().Pods(experimentsDetails.ChaosNamespace).Create(context.Background(), helperPod, v1.CreateOptions{})
	return err

}

// getPodEnv derive all the env required for the helper pod
func getPodEnv(experimentsDetails *experimentTypes.ExperimentDetails, targets string) []apiv1.EnvVar {

	var envDetails common.ENVDetails
	envDetails.SetEnv("TARGETS", targets).
		SetEnv("TOTAL_CHAOS_DURATION", strconv.Itoa(experimentsDetails.ChaosDuration)).
		SetEnv("CHAOS_NAMESPACE", experimentsDetails.ChaosNamespace).
		SetEnv("CHAOSENGINE", experimentsDetails.EngineName).
		SetEnv("CHAOS_UID", string(experimentsDetails.ChaosUID)).
		SetEnv("CONTAINER_RUNTIME", experimentsDetails.ContainerRuntime).
		SetEnv("EXPERIMENT_NAME", experimentsDetails.ExperimentName).
		SetEnv("SOCKET_PATH", experimentsDetails.SocketPath).
		SetEnv("CPU_CORES", experimentsDetails.CPUcores).
		SetEnv("CPU_LOAD", experimentsDetails.CPULoad).
		SetEnv("FILESYSTEM_UTILIZATION_PERCENTAGE", experimentsDetails.FilesystemUtilizationPercentage).
		SetEnv("FILESYSTEM_UTILIZATION_BYTES", experimentsDetails.FilesystemUtilizationBytes).
		SetEnv("NUMBER_OF_WORKERS", experimentsDetails.NumberOfWorkers).
		SetEnv("MEMORY_CONSUMPTION", experimentsDetails.MemoryConsumption).
		SetEnv("VOLUME_MOUNT_PATH", experimentsDetails.VolumeMountPath).
		SetEnv("STRESS_TYPE", experimentsDetails.StressType).
		SetEnv("INSTANCE_ID", experimentsDetails.InstanceID).
		SetEnvFromDownwardAPI("v1", "metadata.name")

	return envDetails.ENV
}

func ptrint64(p int64) *int64 {
	return &p
}

//SetChaosTunables will setup a random value within a given range of values
//If the value is not provided in range it'll setup the initial provided value.
func SetChaosTunables(experimentsDetails *experimentTypes.ExperimentDetails) {
	experimentsDetails.CPUcores = common.ValidateRange(experimentsDetails.CPUcores)
	experimentsDetails.CPULoad = common.ValidateRange(experimentsDetails.CPULoad)
	experimentsDetails.MemoryConsumption = common.ValidateRange(experimentsDetails.MemoryConsumption)
	experimentsDetails.NumberOfWorkers = common.ValidateRange(experimentsDetails.NumberOfWorkers)
	experimentsDetails.FilesystemUtilizationPercentage = common.ValidateRange(experimentsDetails.FilesystemUtilizationPercentage)
	experimentsDetails.FilesystemUtilizationBytes = common.ValidateRange(experimentsDetails.FilesystemUtilizationBytes)
	experimentsDetails.PodsAffectedPerc = common.ValidateRange(experimentsDetails.PodsAffectedPerc)
	experimentsDetails.Sequence = common.GetRandomSequence(experimentsDetails.Sequence)
}
