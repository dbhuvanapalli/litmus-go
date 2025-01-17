package environment

import (
	"strconv"

	clientTypes "k8s.io/apimachinery/pkg/types"

	"github.com/litmuschaos/litmus-go/pkg/types"
	experimentTypes "github.com/litmuschaos/litmus-go/pkg/vmware/vm-poweroff/types"
)

//GetENV fetches all the env variables from the runner pod
func GetENV(experimentDetails *experimentTypes.ExperimentDetails) {
	experimentDetails.ExperimentName = types.Getenv("EXPERIMENT_NAME", "vm-poweroff")
	experimentDetails.ChaosNamespace = types.Getenv("CHAOS_NAMESPACE", "litmus")
	experimentDetails.EngineName = types.Getenv("CHAOSENGINE", "")
	experimentDetails.ChaosDuration, _ = strconv.Atoi(types.Getenv("TOTAL_CHAOS_DURATION", "30"))
	experimentDetails.ChaosInterval, _ = strconv.Atoi(types.Getenv("CHAOS_INTERVAL", "30"))
	experimentDetails.RampTime, _ = strconv.Atoi(types.Getenv("RAMP_TIME", ""))
	experimentDetails.ChaosLib = types.Getenv("LIB", "litmus")
	experimentDetails.ChaosUID = clientTypes.UID(types.Getenv("CHAOS_UID", ""))
	experimentDetails.InstanceID = types.Getenv("INSTANCE_ID", "")
	experimentDetails.ChaosPodName = types.Getenv("POD_NAME", "")
	experimentDetails.AuxiliaryAppInfo = types.Getenv("AUXILIARY_APPINFO", "")
	experimentDetails.TargetContainer = types.Getenv("TARGET_CONTAINER", "")
	experimentDetails.Delay, _ = strconv.Atoi(types.Getenv("STATUS_CHECK_DELAY", "2"))
	experimentDetails.Timeout, _ = strconv.Atoi(types.Getenv("STATUS_CHECK_TIMEOUT", "180"))
	experimentDetails.Sequence = types.Getenv("SEQUENCE", "parallel")
	experimentDetails.VMIds = types.Getenv("APP_VM_MOIDS", "")
	experimentDetails.VcenterServer = types.Getenv("VCENTERSERVER", "")
	experimentDetails.VcenterUser = types.Getenv("VCENTERUSER", "")
	experimentDetails.VcenterPass = types.Getenv("VCENTERPASS", "")
}
