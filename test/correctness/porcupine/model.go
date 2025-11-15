package main

import (
	"fmt"

	"github.com/anishathalye/porcupine"
)

// KVInput represents the input to a KV operation
type KVInput struct {
	Op    string // "get", "put", "delete"
	Key   string
	Value string // only for put
}

// KVOutput represents the output from a KV operation
type KVOutput struct {
	Value string
	Ok    bool
}

// KVModel defines the linearizability model for a key-value store
var KVModel = porcupine.Model{
	Init: func() interface{} {
		// Initial state is an empty map
		return make(map[string]string)
	},
	Step: func(state interface{}, input interface{}, output interface{}) (bool, interface{}) {
		st := state.(map[string]string)
		inp := input.(KVInput)
		out := output.(KVOutput)

		// Create a copy of the state for the next step
		newState := make(map[string]string)
		for k, v := range st {
			newState[k] = v
		}

		switch inp.Op {
		case "get":
			// GET: output should match current state
			val, exists := st[inp.Key]
			if exists {
				if out.Ok && out.Value == val {
					return true, newState
				}
			} else {
				if !out.Ok {
					return true, newState
				}
			}
			return false, state

		case "put":
			// PUT: should succeed and update state
			if out.Ok {
				newState[inp.Key] = inp.Value
				return true, newState
			}
			return false, state

		case "delete":
			// DELETE: should succeed and remove key
			if out.Ok {
				delete(newState, inp.Key)
				return true, newState
			}
			return false, state

		default:
			return false, state
		}
	},
	DescribeOperation: func(input interface{}, output interface{}) string {
		inp := input.(KVInput)
		out := output.(KVOutput)

		switch inp.Op {
		case "get":
			if out.Ok {
				return fmt.Sprintf("get(%s) -> %s", inp.Key, out.Value)
			}
			return fmt.Sprintf("get(%s) -> not found", inp.Key)
		case "put":
			return fmt.Sprintf("put(%s, %s) -> ok", inp.Key, inp.Value)
		case "delete":
			return fmt.Sprintf("delete(%s) -> ok", inp.Key)
		default:
			return fmt.Sprintf("unknown op: %s", inp.Op)
		}
	},
}
