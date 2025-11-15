module github.com/cs598rap/raft-kubernetes

go 1.22

require (
	go.etcd.io/etcd/raft/v3 v3.5.15
	google.golang.org/grpc v1.65.0
	google.golang.org/protobuf v1.34.2
	k8s.io/api v0.30.0
	k8s.io/apimachinery v0.30.0
	k8s.io/client-go v0.30.0
	sigs.k8s.io/controller-runtime v0.18.0
)
