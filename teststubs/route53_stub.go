package teststubs

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
)

// testChangeInfo provides a mock *route53.ChangeInfo struct.
func testChangeInfo() *route53.ChangeInfo {
	return &route53.ChangeInfo{
		Comment:     aws.String("foobar"),
		Id:          aws.String("CHANGE123435"),
		Status:      aws.String("INSYNC"),
		SubmittedAt: aws.Time(time.Now()),
	}
}

// testChangeResourceRecordSetsOutput provides a mock
// *route53.ChangeResourceRecordSetsOutput.
func testChangeResourceRecordSetsOutput() *route53.ChangeResourceRecordSetsOutput {
	return &route53.ChangeResourceRecordSetsOutput{
		ChangeInfo: testChangeInfo(),
	}
}

// testGetChangeOutput provides a mock *route53.GetChangeOutput.
func testGetChangeOutput() *route53.GetChangeOutput {
	return &route53.GetChangeOutput{
		ChangeInfo: testChangeInfo(),
	}
}

// testChangeResourceRecordSets is a stub function for testing the
// route53.DescribeResourceRecordSets function.
func testChangeResourceRecordSets(input *route53.ChangeResourceRecordSetsInput) (*route53.ChangeResourceRecordSetsOutput, error) {
	if *input.HostedZoneId == "bad" {
		return nil, fmt.Errorf("error")
	}
	return testChangeResourceRecordSetsOutput(), nil
}

// testGetChange is a stub function for testing the route53.GetChange
// function.
func testGetChange(input *route53.GetChangeInput) (*route53.GetChangeOutput, error) {
	if *input.Id == "bad" {
		return nil, fmt.Errorf("error")
	}
	return testGetChangeOutput(), nil
}

// CreateTestRoute53Mock returns a mock Route 53 service to use with the
// Route 53 test functions.
func CreateTestRoute53Mock() *route53.Route53 {
	conn := route53.New(session.New(), nil)
	conn.Handlers.Clear()

	conn.Handlers.Send.PushBack(func(r *request.Request) {
		switch p := r.Params.(type) {
		case *route53.ChangeResourceRecordSetsInput:
			out, err := testChangeResourceRecordSets(p)
			if out != nil {
				*r.Data.(*route53.ChangeResourceRecordSetsOutput) = *out
			}
			r.Error = err
		case *route53.GetChangeInput:
			out, err := testGetChange(p)
			if out != nil {
				*r.Data.(*route53.GetChangeOutput) = *out
			}
			r.Error = err
		default:
			panic(fmt.Errorf("Unsupported input type %T", p))
		}
	})
	return conn
}
