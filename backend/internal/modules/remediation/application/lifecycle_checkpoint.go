package application

import "mendry/backend/internal/modules/remediation/domain"

func cloneCheckpointWorkspace(value *domain.CheckpointWorkspace) *domain.CheckpointWorkspace {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneCheckpointValidation(value *domain.CheckpointValidation) *domain.CheckpointValidation {
	if value == nil {
		return nil
	}
	copyValue := *value
	copyValue.Results = append([]domain.CheckpointValidationResult(nil), value.Results...)
	return &copyValue
}

func cloneCheckpointPublication(value *domain.CheckpointPublication) *domain.CheckpointPublication {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
func cloneCheckpointPublicationPolicy(value *domain.CheckpointPublicationPolicy) *domain.CheckpointPublicationPolicy {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneValidationCommands(values map[string]int64) map[string]int64 {
	if len(values) == 0 {
		return nil
	}
	copyValues := make(map[string]int64, len(values))
	for commandID, version := range values {
		copyValues[commandID] = version
	}
	return copyValues
}
