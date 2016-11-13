package teststubs

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// testEC2Reservation provides a test ec2.Reservation struct.
//
// This type is used in the ec2.DescribeInstances() and ec2.RunInstances()
// functions.
func testEC2Reservation() *ec2.Reservation {
	return &ec2.Reservation{
		Instances: []*ec2.Instance{
			&ec2.Instance{
				State: &ec2.InstanceState{
					Code: aws.Int64(16),
					Name: aws.String("running"),
				},
				InstanceId:       aws.String("i-123456789"),
				PrivateIpAddress: aws.String("10.0.0.1"),
				PublicIpAddress:  aws.String("54.0.0.1"),
			},
		},
	}
}

// testDescribeInstancesOutput provides a test ec2.DescribeInstancesOutput
// object.
func testDescribeInstancesOutput() *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []*ec2.Reservation{
			testEC2Reservation(),
		},
	}
}

// testDescribeInstances is a stub function for testing the
// ec2.DescribeInstances function.
func testDescribeInstances(input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	if *input.InstanceIds[0] == "bad" {
		return nil, fmt.Errorf("error")
	}
	return testDescribeInstancesOutput(), nil
}

// CreateTestEC2InstanceMock returns a mock EC2 service to use with the
// instance test functions.
func CreateTestEC2InstanceMock() *ec2.EC2 {
	conn := ec2.New(session.New(), nil)
	conn.Handlers.Clear()

	conn.Handlers.Send.PushBack(func(r *request.Request) {
		switch p := r.Params.(type) {
		case *ec2.DescribeInstancesInput:
			out, err := testDescribeInstances(p)
			if out != nil {
				*r.Data.(*ec2.DescribeInstancesOutput) = *out
			}
			r.Error = err
		default:
			panic(fmt.Errorf("Unsupported input type %T", p))
		}
	})
	return conn
}
