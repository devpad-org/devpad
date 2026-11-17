package wsservice

import "fmt"

func sidecarContainerName(workspaceID int64, serviceType ServiceType) string {
	return fmt.Sprintf("devpad-svc-%d-%s", workspaceID, serviceType)
}
