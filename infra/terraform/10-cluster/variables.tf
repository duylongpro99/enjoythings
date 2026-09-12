variable "cluster_name" {
  description = "kind cluster name. The kubectl context becomes kind-<name>."
  type        = string
  default     = "enjoythings"
}

variable "node_image" {
  description = "kindest/node image, which pins the Kubernetes version. null uses the provider default."
  type        = string
  default     = null
}

variable "gateway_node_port" {
  description = "NodePort of the gateway Service (applications.gateway.nodePort in the Helm chart)."
  type        = number
  default     = 30080
}

variable "gateway_host_port" {
  description = "Port on your machine that forwards to gateway_node_port."
  type        = number
  default     = 18080
}
