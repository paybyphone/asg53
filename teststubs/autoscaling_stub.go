package teststubs

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
)

// testCompleteLifecycleAction is a stub function for testing the
// autoscaling.CompleteLifecycleAction function.
//
// Note that the unstubbed function does not return anything useful, so we
// don't try to mock anything here.
func testCompleteLifecycleAction(input *autoscaling.CompleteLifecycleActionInput) (*autoscaling.CompleteLifecycleActionOutput, error) {
	if *input.LifecycleActionResult == "bad" {
		return nil, fmt.Errorf("error")
	}
	return &autoscaling.CompleteLifecycleActionOutput{}, nil
}

// CreateTestAutoscalingMock returns a mock autoscaling service to use with the
// autoscaling test functions.
func CreateTestAutoScalingMock() *autoscaling.AutoScaling {
	conn := autoscaling.New(session.New(), nil)
	conn.Handlers.Clear()

	conn.Handlers.Send.PushBack(func(r *request.Request) {
		switch p := r.Params.(type) {
		case *autoscaling.CompleteLifecycleActionInput:
			out, err := testCompleteLifecycleAction(p)
			if out != nil {
				*r.Data.(*autoscaling.CompleteLifecycleActionOutput) = *out
			}
			r.Error = err
		default:
			panic(fmt.Errorf("Unsupported input type %T", p))
		}
	})
	return conn
}
