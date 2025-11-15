module github.com/cs598rap/raft-kubernetes/test/correctness/porcupine

go 1.22

require (
	github.com/anishathalye/porcupine v0.1.6
	github.com/cs598rap/raft-kubernetes/server v0.0.0-00010101000000-000000000000
)

replace github.com/cs598rap/raft-kubernetes/server => ../../../server
