package runtime

import "testing"

func TestDesired(t *testing.T) {
	for _, tc := range []struct {
		name  string
		queue QueueStats
		min   int
		max   int
		exp   int
	}{
		{
			name: "nothing to do",
			max:  3,
			exp:  0,
		},
		{
			name: "one per visible message",
			queue: QueueStats{
				Visible: 2,
			},
			max: 3,
			exp: 2,
		},
		{
			name: "visible capped at max",
			queue: QueueStats{
				Visible: 10,
			},
			max: 3,
			exp: 3,
		},
		{
			name: "one per pending message",
			queue: QueueStats{
				Pending: 2,
			},
			max: 3,
			exp: 2,
		},
		{
			name: "pending plus visible",
			queue: QueueStats{
				Pending: 1,
				Visible: 1,
			},
			max: 3,
			exp: 2,
		},
		{
			name: "pending plus visible capped at max",
			queue: QueueStats{
				Pending: 2,
				Visible: 2,
			},
			max: 3,
			exp: 3,
		},
		{
			name: "pending alone capped at max",
			queue: QueueStats{
				Pending: 5,
			},
			max: 3,
			exp: 3,
		},
		{
			name: "min workers with nothing to do",
			min:  1,
			max:  3,
			exp:  1,
		},
		{
			name: "min workers below demand",
			queue: QueueStats{
				Visible: 2,
			},
			min: 1,
			max: 3,
			exp: 2,
		},
		{
			name: "min wins over max",
			min:  5,
			max:  3,
			exp:  5,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act := desired(tc.queue, tc.min, tc.max)
			if act != tc.exp {
				t.Errorf(
					"desired(%+v, min=%d, max=%d): %d; want %d",
					tc.queue, tc.min, tc.max, act, tc.exp,
				)
			}
		})
	}
}
