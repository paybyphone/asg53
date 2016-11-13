package main

import (
	"fmt"
	"log"
	"os"
	"reflect"
	"testing"

	"github.com/paybyphone/asg53/teststubs"
)

// testMessageJSON is a test SNS message in JSON form.
//
/// Metadata is mocked separately.
const testMessageJSON = `
{
  "EC2InstanceId": "i-123456789",
  "AutoScalingGroupName": "ASGName",
  "LifecycleHookName": "Lifecycle",
  "LifecycleActionToken": "Token"
}
`

// testMetadataJSON is a test SNS message in JSON form. The outer event is not
// currently mocked.
const testMetadataJSON = `
{
  "HostedZoneID": "ABCDEF0123456789",
  "Changes": [
    {
      "Action": "CREATE",
      "ResourceRecordSet": {
        "Name": "{{.InstanceID}}.example.com.",
        "TTL": 3600,
        "Type": "A",
        "ResourceRecords": [
          {
            "Value": "{{.InstancePublicIPAddress}}"
          }
        ]
      }
    },
    {
      "Action": "CREATE",
      "ResourceRecordSet": {
        "Name": "www.example.com.",
        "TTL": 3600,
        "Type": "CNAME",
        "ResourceRecords": [
          {
            "Value": "{{.InstanceID}}.example.com."
          }
        ]
      }
    }
  ]
}
`

// testAwsClient returns a mock *awsClient with the services stubbed from the
// teststubs package.
func testAwsClient() *awsClient {
	client := awsClient{}
	client.EC2 = teststubs.CreateTestEC2InstanceMock()
	client.AutoScaling = teststubs.CreateTestAutoScalingMock()
	client.Route53 = teststubs.CreateTestRoute53Mock()

	return &client
}

func TestFetchEC2InstanceData(t *testing.T) {
	instanceID := "i-123456789"
	client := testAwsClient()

	instance, err := client.FetchEC2InstanceData(instanceID)
	if err != nil {
		t.Fatalf("Bad: %v", err)
	}

	if *instance.InstanceId != instanceID {
		t.Fatalf("Expected InstanceId to be %s, got %s", instanceID, *instance.InstanceId)
	}
}

func TestFetchEC2InstanceData_shouldError(t *testing.T) {
	instanceID := "bad"
	client := testAwsClient()

	_, err := client.FetchEC2InstanceData(instanceID)
	if err == nil {
		t.Fatal("Expected error, got none")
	}
}

func TestSendRoute53ChangeBatch(t *testing.T) {
	metadata, err := parseSNSMetadata([]byte(testMetadataJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	batch := metadata.Changes
	zoneID := metadata.HostedZoneID

	client := testAwsClient()

	if err := client.SendRoute53ChangeBatch(zoneID, batch); err != nil {
		t.Fatalf("Expected no error, got #%v", err)
	}
}

func TestSendRoute53ChangeBatch_shouldError(t *testing.T) {
	metadata, err := parseSNSMetadata([]byte(testMetadataJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	batch := metadata.Changes
	zoneID := "bad"

	client := testAwsClient()

	if err := client.SendRoute53ChangeBatch(zoneID, batch); err == nil {
		t.Fatal("Expected error, got none")
	}
}

func TestWaitForRoute53Sync(t *testing.T) {
	id := "foobar"
	client := testAwsClient()

	if err := client.WaitForRoute53Sync(id); err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
}

func TestWaitForRoute53Sync_shouldError(t *testing.T) {
	id := "bad"
	client := testAwsClient()

	if err := client.WaitForRoute53Sync(id); err == nil {
		t.Fatal("Expected error, got none")
	}
}

func TestCompleteAutoscalingAction(t *testing.T) {
	message, err := parseInnerSNSMessage([]byte(testMessageJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	result := "CONTINUE"

	client := testAwsClient()

	if err := client.CompleteAutoscalingAction(message, result); err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
}

func TestCompleteAutoscalingAction_shouldError(t *testing.T) {
	message, err := parseInnerSNSMessage([]byte(testMessageJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	result := "bad"

	client := testAwsClient()

	if err := client.CompleteAutoscalingAction(message, result); err == nil {
		t.Fatal("Expected error, got none")
	}
}

func TestPopulate(t *testing.T) {
	instanceID := "i-123456789"
	client := testAwsClient()

	metadata, err := parseSNSMetadata([]byte(testMetadataJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	expected := &instanceData{
		Client:                   client,
		HostedZoneID:             metadata.HostedZoneID,
		Batch:                    metadata.Changes,
		InstanceID:               "i-123456789",
		InstancePrivateIPAddress: "10.0.0.1",
		InstancePublicIPAddress:  "54.0.0.1",
	}

	actual, err := populate(client, instanceID, metadata.HostedZoneID, metadata.Changes)
	if err != nil {
		t.Fatalf("Bad: %v", err)
	}

	if reflect.DeepEqual(expected, actual) == false {
		t.Fatalf("Expected %#v, got %#v", expected, actual)
	}
}

func TestWriteTemplateFields(t *testing.T) {
	message, err := parseInnerSNSMessage([]byte(testMessageJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	metadata, err := parseSNSMetadata([]byte(testMetadataJSON))
	if err != nil {
		panic(fmt.Errorf("Bad JSON in test: %v", err))
	}

	batch := metadata.Changes
	instanceID := message.EC2InstanceID

	client := testAwsClient()
	data, err := populate(client, instanceID, metadata.HostedZoneID, metadata.Changes)
	if err != nil {
		t.Fatalf("Bad: %v", err)
	}

	if err := data.WriteTemplateFields(); err != nil {
		t.Fatalf("Bad: %v", err)
	}

	if *batch[0].ResourceRecordSet.Name != "i-123456789.example.com." {
		t.Fatalf("Expected batch[0].ResourceRecordSet.Name to be i-123456789.example.com., got %s", *batch[0].ResourceRecordSet.Name)
	}
	if *batch[0].ResourceRecordSet.ResourceRecords[0].Value != "54.0.0.1" {
		t.Fatalf("Expected batch[0].ResourceRecordSet.ResourceRecords[0].Value to be 54.0.0.1, got %s", *batch[0].ResourceRecordSet.ResourceRecords[0].Value)
	}
	if *batch[1].ResourceRecordSet.ResourceRecords[0].Value != "i-123456789.example.com." {
		t.Fatalf("Expected batch[1].ResourceRecordSet.ResourceRecords[0].Value to be i-123456789.example.com., got %s", *batch[1].ResourceRecordSet.ResourceRecords[0].Value)
	}
}

func TestMain(m *testing.M) {
	log.SetOutput(os.Stderr)
	os.Exit(m.Run())
}
