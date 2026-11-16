package tests

import (
	"os"
	"strconv"
	"strings"
)

func childAlive(pid int) bool {
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if e != nil {
		return false
	}
	i := strings.LastIndex(string(b), ") ")
	return i >= 0 && len(b) > i+2 && b[i+2] != 'Z'
}
