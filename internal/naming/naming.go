package naming

import (
	"fmt"
	"regexp"
	"sort"
)

var hollowNameRe = regexp.MustCompile(`^hollow-node-(\d+)$`)

func HollowNodeName(n int) string { return fmt.Sprintf("hollow-node-%d", n) }

// NextAvailableNodeNumber returns the next index after the highest hollow-node-<n>
func NextAvailableNodeNumber(existing []string) int {
	nums := []int{}
	for _, name := range existing {
		m := hollowNameRe.FindStringSubmatch(name)
		if len(m) == 2 {
			var n int
			fmt.Sscanf(m[1], "%d", &n)
			nums = append(nums, n)
		}
	}
	if len(nums) == 0 {
		return 1
	}
	sort.Ints(nums)
	return nums[len(nums)-1] + 1
}
