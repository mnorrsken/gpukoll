package kube

import "time"

// Node holds the fields of a Kubernetes Node that gpukoll reads.
type Node struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Capacity   map[string]string `json:"capacity"`
		Conditions []Condition       `json:"conditions"`
	} `json:"status"`
}

// Condition is a node condition such as Ready.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// Pod holds the fields of a Kubernetes Pod that gpukoll reads.
type Pod struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		NodeName       string      `json:"nodeName"`
		Containers     []Container `json:"containers"`
		InitContainers []Container `json:"initContainers"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// Container holds a container's resource requests and limits.
type Container struct {
	Name      string `json:"name"`
	Resources struct {
		Requests map[string]string `json:"requests"`
		Limits   map[string]string `json:"limits"`
	} `json:"resources"`
}
