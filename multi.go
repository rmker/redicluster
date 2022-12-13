package redicluster

import (
	"errors"
	"fmt"
)

// Supports MSET command for redis cluster through pipeLiner
func multiset(c *redirconn, args ...interface{}) (interface{}, error) {
	var (
		orderSlots []int
		res        interface{}
	)
	if len(args) <= 0 {
		return nil, nil
	}
	cmdMap := make(map[int][]interface{})
	for i := 0; i < len(args); i = i + 2 {
		vk := i + 1
		if vk >= len(args) {
			// args length exeption for mset
			return nil, errors.New("args length exeption for mset")
		}
		key := fmt.Sprintf("%s", args[i])
		slot := Slot(key)
		_, exist := cmdMap[slot]
		if !exist {
			cmdMap[slot] = []interface{}{key, args[vk]}
		} else {
			cmdMap[slot] = append(cmdMap[slot], key, args[vk])
		}
	}

	pipeLiner := newPipeliner(c.cp)
	defer pipeLiner.Close()
	for slot := range cmdMap {
		err := pipeLiner.send("MSET", cmdMap[slot]...)
		if err != nil {
			return nil, err
		}
		orderSlots = append(orderSlots, slot)
	}
	err := pipeLiner.flush()
	if err != nil {
		return nil, err
	}
	for range orderSlots {
		reply, err := pipeLiner.receive()
		if err != nil {
			return nil, err
		}
		res = reply
	}
	return res, nil
}

// Supports MGET command for redis cluster through pipeLiner
func multiget(c *redirconn, args ...interface{}) (interface{}, error) {
	var (
		orderSlots []int
		res        []interface{}
		keys       []string
	)
	if len(args) <= 0 {
		return nil, nil
	}
	cmdMap := make(map[int][]interface{})
	for _, arg := range args {
		key := fmt.Sprintf("%s", arg)
		keys = append(keys, key)
		slot := Slot(key)
		_, exist := cmdMap[slot]
		if !exist {
			cmdMap[slot] = []interface{}{key}
		} else {
			cmdMap[slot] = append(cmdMap[slot], key)
		}
	}
	pipeLiner := newPipeliner(c.cp)
	defer pipeLiner.Close()
	for slot := range cmdMap {
		err := pipeLiner.send("MGET", cmdMap[slot]...)
		if err != nil {
			return nil, err
		}
		orderSlots = append(orderSlots, slot)
	}
	err := pipeLiner.flush()
	if err != nil {
		return nil, err
	}

	resMap := make(map[string]interface{})
	for _, slot := range orderSlots {
		reply, err := pipeLiner.receive()
		if err != nil {
			return nil, err
		}
		keyCount := len(cmdMap[slot])
		replySlice, ok := reply.([]interface{})
		validReply := false
		if ok && keyCount == len(replySlice) {
			validReply = true
		}
		for i := 0; i < keyCount; i++ {
			key := fmt.Sprintf("%s", cmdMap[slot][i])
			if validReply {
				resMap[key] = replySlice[i]
			} else {
				resMap[key] = nil
			}
		}
	}
	for i := range keys {
		res = append(res, resMap[keys[i]])
	}
	return res, nil
}
