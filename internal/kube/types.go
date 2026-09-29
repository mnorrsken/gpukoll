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

// EndpointSlice holds the fields of a discovery.k8s.io/v1 EndpointSlice
// that gpukoll reads.
type EndpointSlice struct {
	Endpoints []struct {
		Addresses []string `json:"addresses"`
		NodeName  string   `json:"nodeName"`
		TargetRef *struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"targetRef"`
		Conditions struct {
			Ready *bool `json:"ready"`
		} `json:"conditions"`
	} `json:"endpoints"`
	Ports []struct {
		Name string `json:"name"`
		Port int    `json:"port"`
	} `json:"ports"`
}
