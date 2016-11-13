package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/private/waiter"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/eawsy/aws-lambda-go/service/lambda/runtime"
)

// eventNotification represents an abridged version of a SNS notification
// through Lambda.
type eventNotification struct {
	// The event records.
	Records []eventRecord
}

// eventRecord represents an abridged version of an SNS notification
// record through Lambda.
type eventRecord struct {
	// The SNS structure.
	Sns snsEvent
}

// snsEvent represents an abridged version of an SNS notification
// event through Lambda.
type snsEvent struct {
	// The SNS message. This is a string value, and must be interpolated
	// further into a JSON object of type snsMessage.
	Message string
}

// snsMessage represents an abridged version of an SNS notification
// event through Lambda.
type snsMessage struct {
	// The SNS event type. If a test notification is received, this will read
	// "autoscaling:TEST_NOTIFICATION" and most other fields will be empty.
	Event string

	// The EC2 instance ID from the lifecycle event.
	EC2InstanceID string `json:"EC2InstanceId"`

	// The auto scaling group name the event was called for.
	AutoScalingGroupName string

	// The name of the lifecycle hook that the event was called for.
	LifecycleHookName string

	// The action token for this lifecycle hook event.
	LifecycleActionToken string

	// The metadata supplied to the lifecycle hook. This contains the
	// arguments for the operation. This needs to be parsed into a messageArgs
	// struct.
	NotificationMetadata string
}

// messageArgs supplies the arguments and Route 53 changes to the function in
// the form of SNS metadata.
//
// Example:
//
//   {
//   	"HostedZoneID": "ABCDEF0123456789",
//   	"Changes": [
//   		{
//   			"Action": "CREATE",
//   			"ResourceRecordSet": {
//   				"Name": "{{.InstanceID}}.example.com.",
//   				"TTL": 3600,
//   				"Type": "A",
//   				"ResourceRecords": [
//   					{
//   						"Value": "{{.InstancePublicIPAddress}}"
//   					}
//   				]
//   			}
//   		},
//   		{
//   			"Action": "CREATE",
//   			"ResourceRecordSet": {
//   				"Name": "www.example.com.",
//   				"TTL": 3600,
//   				"Type": "CNAME",
//   				"ResourceRecords": [
//   					{
//   						"Value": "{{.InstanceID}}.example.com."
//   					}
//   				]
//   			}
//   		}
//   	]
//   }
//
// "Changes" within the example is a literal JSON translation of a Route 53
// change batch request. For more information, see
// http://docs.aws.amazon.com/Route53/latest/APIReference/API_Change.html#Route53-Type-Change-ResourceRecordSet
// Or the specific Go struct at
// http://docs.aws.amazon.com/sdk-for-go/api/service/route53/#Change.
//
// Within "Changes", you can use the following Go template fields and they
// will be interpolated for you:
//
//   * {{.InstanceID}}, for the instance ID
//   * {{.InstancePrivateIPAddress}}, for the instance's private IP address
//   * {{.InstancePublicIPAddress}}, for the instance's public IP address
//   * {{.ExistingRDataValue [set] [record]}}, to get the existing RDATA
//     on a resource record set. This function operates on the existing
//     change set, operating on the specific fields of the resource record set
//     asked for. This means that whether or not a properly rendered Name
//     field exists depends on where this function is called - if called too early
//     on a field that has not yet been iterated on, the templated data will
//     be incomplete. Lookups that result in no data
//     returned, an out of range value index, or a Route 53 API error will
//     cause an error and fail the hook.
//
// If for some reason your changebatch results in an error, the function will
// fail and ABANDON the hook.
//
// Note that on termination events, IP address values will be rendered as
// empty strings, so take care when using DELETE events that you don't
// attempt to delete a non-existent, or even worse, an incorrect, record.
// Use ExistingRDataValue to locate the existing resource record for the
// value, instead:
//
//   {
//   	"HostedZoneID": "ABCDEF0123456789",
//   	"Changes": [
//   		{
//   			"Action": "DELETE",
//   			"ResourceRecordSet": {
//   				"Name": "{{.InstanceID}}.example.com.",
//   				"TTL": 3600,
//   				"Type": "A",
//   				"ResourceRecords": [
//   					{
//   						"Value": "{{.ExistingRDataValue 0 0}}"
//   					}
//   				]
//   			}
//   		}
//   	]
//   }
type messageArgs struct {
	// The hosted zone ID to operate on.
	HostedZoneID string

	// A Route 53 change batch. See the struct's
	// documentation for more information on setting this value.
	Changes []*route53.Change
}

// awsClient is an AWS service matrix for resources that we will need through
// the course of the workflow. It also contains information about this invocation of
type awsClient struct {
	// The AutoScaling connection.
	AutoScaling *autoscaling.AutoScaling

	// The EC2 connection.
	EC2 *ec2.EC2

	// The Route 53 connection.
	Route53 *route53.Route53
}

// newAWSConn returns an initialized AWS connection matrix. An error is
// returned if there is some sort of issue.
func newAWSClient() (*awsClient, error) {
	conn := awsClient{}
	log.Println("Setting up AWS connections.")

	sess, err := session.NewSession()
	if err != nil {
		return nil, fmt.Errorf("Error creating AWS session: %v", err)
	}

	conn.EC2 = ec2.New(sess)
	conn.AutoScaling = autoscaling.New(sess)
	conn.Route53 = route53.New(sess)

	return &conn, nil
}

// FetchEC2InstanceData returns an *ec2.Instance with the loaded instance ID.
func (c *awsClient) FetchEC2InstanceData(instanceID string) (*ec2.Instance, error) {
	log.Printf("Fetching EC2 instance data for ID: %s", instanceID)
	params := &ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceID}),
	}

	resp, err := c.EC2.DescribeInstances(params)
	if err != nil {
		return nil, fmt.Errorf("Error fetching instance data: %v", err)
	}

	if len(resp.Reservations) < 1 || len(resp.Reservations[0].Instances) < 1 {
		return nil, fmt.Errorf("Cannot find instance ID %s", instanceID)
	}

	return resp.Reservations[0].Instances[0], nil
}

// FindRoute53ResourceRecord looks for a specific resource record Name and
// Type within route 53 for a specific hosted zone. Its resource record
// values are returned. If the record is not found, this function returns an
// error.
func (c *awsClient) FindRoute53ResourceRecord(zoneID, name, rrType string) ([]*route53.ResourceRecord, error) {
	log.Printf("Looking for resource record set %s %s in zone ID: %s", name, rrType, zoneID)

	params := &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(zoneID),
		MaxItems:        aws.String("1"),
		StartRecordName: aws.String(name),
		StartRecordType: aws.String(rrType),
	}

	resp, err := c.Route53.ListResourceRecordSets(params)
	if err != nil {
		return nil, fmt.Errorf("Error locating resource record: %v", err)
	}

	if len(resp.ResourceRecordSets) < 1 {
		return nil, fmt.Errorf("Resource record set %s %s not found", name, rrType)
	}

	return resp.ResourceRecordSets[0].ResourceRecords, nil
}

// SendRoute53ChangeBatch sends the configured change batch to Route 53.
// The function also waits for the batch to be fully synced before returning.
func (c *awsClient) SendRoute53ChangeBatch(zoneID string, batch []*route53.Change) error {
	log.Printf("Sending Route53 change sets to zone ID: %s", zoneID)
	params := &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
		ChangeBatch: &route53.ChangeBatch{
			Changes: batch,
		},
	}

	resp, err := c.Route53.ChangeResourceRecordSets(params)
	if err != nil {
		return fmt.Errorf("Error sending change batch: %v", err)
	}

	// Wait for the change to sync.
	return c.WaitForRoute53Sync(*resp.ChangeInfo.Id)
}

// WaitForRoute53Sync waits until a Route 53 change batch is INSYNC, taking
// the change batch ID.
//
// This is a re-implmentation of route53.WaitUntilResourceRecordSetsChanged, with a
// much shorter sleep interval (the AWS SDK version is 30 seconds).
func (c *awsClient) WaitForRoute53Sync(changeID string) error {
	log.Printf("Waiting for change ID %s to sync", changeID)

	start := time.Now()

	params := &route53.GetChangeInput{
		Id: aws.String(changeID),
	}

	waiterCfg := waiter.Config{
		Operation:   "GetChange",
		Delay:       5,
		MaxAttempts: 24,
		Acceptors: []waiter.WaitAcceptor{
			{
				State:    "success",
				Matcher:  "path",
				Argument: "ChangeInfo.Status",
				Expected: "INSYNC",
			},
		},
	}

	w := waiter.Waiter{
		Client: c.Route53,
		Input:  params,
		Config: waiterCfg,
	}

	stop := make(chan bool)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				time.Sleep(time.Second * 5)
				elapsed := time.Since(start)
				log.Printf("Still waiting for change ID %s, elapsed time %fs", changeID, elapsed.Seconds())
			}
		}
	}()

	err := w.Wait()
	stop <- true
	return err
}

// CompleteAutoscalingAction sends the ABANDON or CONTINUE result to the
// auto scaling lifecycle ID.
func (c *awsClient) CompleteAutoscalingAction(messageData snsMessage, result string) error {
	log.Printf("Sending result %s for action token %s", result, messageData.LifecycleActionToken)

	params := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  aws.String(messageData.AutoScalingGroupName),
		InstanceId:            aws.String(messageData.EC2InstanceID),
		LifecycleActionResult: aws.String(result),
		LifecycleActionToken:  aws.String(messageData.LifecycleActionToken),
		LifecycleHookName:     aws.String(messageData.LifecycleHookName),
	}

	_, err := c.AutoScaling.CompleteLifecycleAction(params)
	if err != nil {
		log.Printf("Error performing autoscaling action: %v", err)
	}
	return err
}

// instanceData represents the instance data available to be templated.
type instanceData struct {
	// An AWS client instance.
	Client *awsClient

	// The route 53 hosted zone to operate on.
	HostedZoneID string

	// The route 53 change batch we are operating on.
	Batch []*route53.Change

	// The instance ID.
	InstanceID string

	// The private IP address of the instance.
	InstancePrivateIPAddress string

	// The public IP address of the instance.
	InstancePublicIPAddress string
}

// populate returns an instanceData struct with the fields that we need set.
func populate(client *awsClient, instanceID, hostedZoneID string, batch []*route53.Change) (*instanceData, error) {
	data := instanceData{
		Client:       client,
		HostedZoneID: hostedZoneID,
		Batch:        batch,
	}

	instance, err := data.Client.FetchEC2InstanceData(instanceID)
	if err != nil {
		return &data, err
	}

	log.Printf("Instance data returned: %#v", instance)

	data.InstanceID = instanceID

	// Note that on termination events, IP address values will either have zero
	// values or be missing altogether. This is okay, because Route53 ignores
	// resource record set values when processing a DELETE change. The
	// operator should be aware of this when writing the template.
	if instance.PrivateIpAddress != nil && *instance.PrivateIpAddress != "" {
		data.InstancePrivateIPAddress = *instance.PrivateIpAddress
	}
	if instance.PublicIpAddress != nil && *instance.PublicIpAddress != "" {
		data.InstancePublicIPAddress = *instance.PublicIpAddress
	}

	return &data, nil
}

// ExistingRDataValue returns the existing resource record (that is, currently
// in Route 53) specified by rDataIndex, for a resource record set in the
// change batch. The specific record searched on is specified by rrSetIndex.
//
// This function returns an error if the resource record set does not exist,
// or if the requested resource record index is out of range.
func (d *instanceData) ExistingRDataValue(rrSetIndex, rDataIndex int) (string, error) {
	if len(d.Batch)-1 < rrSetIndex {
		return "", fmt.Errorf("Requested rrSet index of %d out of range", rrSetIndex)
	}
	rrSet := d.Batch[rrSetIndex]
	rData, err := d.Client.FindRoute53ResourceRecord(d.HostedZoneID, *rrSet.ResourceRecordSet.Name, *rrSet.ResourceRecordSet.Type)
	if err != nil {
		return "", err
	}
	if len(rData)-1 < rDataIndex {
		return "", fmt.Errorf("Requested rDataIndex index of %d out of range", rDataIndex)
	}
	rDataItem := rData[rDataIndex]
	return *rDataItem.Value, nil
}

// WriteTemplateFields iterates through all the
// items in the batch, and writes out template fields in
// ResourceRecordSet.Name and all fields in ResourceRecordSet.Records.
func (d *instanceData) WriteTemplateFields() error {
	log.Println("Writing template values for change batch")
	for n, rrSet := range d.Batch {
		nameRendered := &bytes.Buffer{}
		valuesRendered := []string{}

		nameTemplate, err := template.New(fmt.Sprintf("RR Set #%d .Name", n)).Parse(*rrSet.ResourceRecordSet.Name)
		if err != nil {
			return err
		}
		if err := nameTemplate.Execute(nameRendered, d); err != nil {
			return err
		}

		rrSet.ResourceRecordSet.Name = aws.String(nameRendered.String())

		for x, resourceRecord := range rrSet.ResourceRecordSet.ResourceRecords {
			valueRendered := &bytes.Buffer{}
			valueTemplate, err := template.New(fmt.Sprintf("RR Set #%d .Records.Value #%d", n, x)).Parse(*resourceRecord.Value)

			if err != nil {
				return err
			}
			if err := valueTemplate.Execute(valueRendered, d); err != nil {
				return err
			}
			resourceRecord.Value = aws.String(valueRendered.String())
			valuesRendered = append(valuesRendered, valueRendered.String())
		}

		log.Printf("Record written: %s %d %s %s", nameRendered.String(), *rrSet.ResourceRecordSet.TTL, *rrSet.ResourceRecordSet.Type, strings.Join(valuesRendered, ","))
	}
	return nil
}

// parseOuterEvent parses the outer event that comes in from AWS Lambda and
// converts it into an eventNotification. This then needs to be further
// parsed to get the inner SNS message, and from there, the metadata.
func parseOuterEvent(raw []byte) (eventNotification, error) {
	log.Printf("Raw event JSON data: %s", string(raw))
	parsed := eventNotification{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("Error parsing event JSON: %v", err)
		return parsed, err
	}
	return parsed, nil
}

// parseInnerSNSMessage parses the inner SNS message that comes in from the outer
// AWS Lambda event. A snsMessage is returned. The metadata is a string value
// and needs to be further parsed from this return data.
func parseInnerSNSMessage(raw []byte) (snsMessage, error) {
	log.Printf("Raw SNS message JSON data: %s", string(raw))
	parsed := snsMessage{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("Error parsing SNS message JSON: %v", err)
		return parsed, err
	}
	return parsed, nil
}

// parseSNSMetadata parses the inner SNS message's metadata into the
// function's Route 53 ID, changes, and other parameters.
func parseSNSMetadata(raw []byte) (messageArgs, error) {
	log.Printf("Raw metadata JSON data: %s", string(raw))
	parsed := messageArgs{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		log.Printf("Error parsing metadata JSON: %v", err)
		return parsed, err
	}
	return parsed, nil
}

// parseFullEvent parses the event, inner SNS message, and the metadata to
// return the relevant structs.
func parseFullEvent(raw []byte) (snsMessage, messageArgs, error) {
	parsedEvent := eventNotification{}
	parsedMessage := snsMessage{}
	parsedMetadata := messageArgs{}
	var err error

	parsedEvent, err = parseOuterEvent(raw)
	if err != nil {
		return parsedMessage, parsedMetadata, err
	}

	if len(parsedEvent.Records) < 1 {
		return parsedMessage, parsedMetadata, errors.New("Parsed event contains no records")
	}

	parsedMessage, err = parseInnerSNSMessage([]byte(parsedEvent.Records[0].Sns.Message))
	if err != nil {
		return parsedMessage, parsedMetadata, err
	}

	if parsedMessage.Event == "autoscaling:TEST_NOTIFICATION" {
		// This is a test notification and will not have any metadata - return now.
		return parsedMessage, parsedMetadata, nil
	}

	parsedMetadata, err = parseSNSMetadata([]byte(parsedMessage.NotificationMetadata))
	if err != nil {
		return parsedMessage, parsedMetadata, err
	}

	return parsedMessage, parsedMetadata, nil
}

// handle is our handler function for Lambda.
//
// Depending on the reasons for erroring out, we need to not return an error
// from the function so that Lambda doesn't try running it again. This is
// generally after records may have been written (so after sending the change
// batch, and sending the final CONTINUE action). Test notifications are also
// dropped on the floor.
func handle(evt json.RawMessage, ctx *runtime.Context) (interface{}, error) {
	log.Println("asg53 starting.")

	message, args, err := parseFullEvent(evt)
	if err != nil {
		return nil, err
	}

	client, err := newAWSClient()
	if err != nil {
		log.Printf("Error loading AWS client: %v", err)
		return nil, err
	}

	if message.Event == "autoscaling:TEST_NOTIFICATION" {
		log.Println("This is a test notification - ignoring and exiting.")
		return nil, nil
	}

	log.Printf("Event triggered for %s:%s:%s", message.AutoScalingGroupName, message.EC2InstanceID, message.LifecycleHookName)

	data, err := populate(client, message.EC2InstanceID, args.HostedZoneID, args.Changes)
	if err != nil {
		log.Printf("Error fetching instance information: %v", err)
		return nil, err
	}

	if err := data.WriteTemplateFields(); err != nil {
		log.Printf("Error writing template values: %v", err)
		return nil, err
	}

	if err := client.SendRoute53ChangeBatch(args.HostedZoneID, args.Changes); err != nil {
		log.Printf("Error sending change batch to Route 53: %v", err)
		client.CompleteAutoscalingAction(message, "ABANDON")
		return nil, nil
	}

	log.Printf("Completed Route 53 action, sending continue event")
	client.CompleteAutoscalingAction(message, "CONTINUE")
	return nil, nil
}

func init() {
	runtime.HandleFunc(handle)
}

func main() {}
