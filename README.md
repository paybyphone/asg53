# asg53

asg53 is a tool for [AWS Lambda][1] that allows one to process
[EC2 Auto-Scaling][2] events and make modifications in [Route 53][3]. It is
intended for use with SNS, and uses data-driven JSON representations of Route 53
change batches that can be customized with [Go templates][4], supplied as
notification metadata.

## Building

```
go get github.com/paybyphone/asg53
docker run --rm -v $GOPATH:$GOPATH -e GOPATH=$GOPATH -w `pwd` eawsy/aws-lambda-go
```

After the build is complete, upload the `handler.zip` file to Lambda.

## Usage

First you will want to read up on how to configure [Lifecycle Hooks][5] for Auto
Scaling. This is a moderately complex process that may be documented here later.

The Lambda function's role needs the following policy attached to it:

```
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Sid": "",
            "Effect": "Allow",
            "Action": [
                "route53:ListResourceRecordSets",
                "route53:ChangeResourceRecordSets"
            ],
            "Resource": "arn:aws:route53:::hostedzone/HOSTEDZONEID"
        },
        {
            "Sid": "",
            "Effect": "Allow",
            "Action": "route53:GetChange",
            "Resource": "arn:aws:route53:::change/*"
        },
        {
            "Sid": "",
            "Effect": "Allow",
            "Action": "ec2:DescribeInstances",
            "Resource": "*"
        },
        {
            "Sid": "",
            "Effect": "Allow",
            "Action": "autoscaling:CompleteLifecycleAction",
            "Resource": "*"
        },
        {
            "Sid": "",
            "Effect": "Allow",
            "Action": [
                "logs:PutLogEvents",
                "logs:DescribeLogStreams",
                "logs:CreateLogStream",
                "logs:CreateLogGroup"
            ],
            "Resource": "arn:aws:logs:*:*:*"
        }
    ]
}
```

Next, when creating your lifecycle hooks, you will want to structure your
notification metadata in the JSON format below. For example, this metadata set
could create a route 53 resource record for an instance while it's being
launched:

```
{
  "HostedZoneID": "HOSTEDZONEID",
  "Changes": [
    {
      "Action": "UPSERT",
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
    }
  ]
}
```

And this can be added to a termination event to delete the record.

```
{
  "HostedZoneID": "HOSTEDZONEID",
  "Changes": [
    {
      "Action": "DELETE",
      "ResourceRecordSet": {
        "Name": "{{.InstanceID}}.example.com.",
        "TTL": 3600,
        "Type": "A",
        "ResourceRecords": [
          {
            "Value": "{{.ExistingRDataValue 0 0}}"
          }
        ]
      }
    }
  ]
}
```

## How it Works

In the metadata you are supplying the hosted zone ID to act on, in addition to a
Route 53 change batch. The same rules apply to the latter as they would when
using the CLI, so see [there][6] for more details.

The function can operate on both `autoscaling:EC2_INSTANCE_LAUNCHING` or
`autoscaling:EC2_INSTANCE_TERMINATING` events, or any other autoscaling
lifecycle event, for that matter. The function does not take any specific action
for a certain lifecycle event. The only caveat is that you need to be cognizant
of when you are processing termination events, as certain template fields won't
be available (see below).

## Template Reference

The data is driven by Go tempalte values (using a double-curly bracer closure -
`{{}}`) that allow you to access specific fields related to the instance.

Note that fields are interpolated on `Name` and `Value` fields only (the latter
in the `ResourceRecords` list).

Current fields are:

 * `{{.InstanceID}}`, for the instance ID
 * `{{.InstancePrivateIPAddress}}`, for the instance's private IP address
 * `{{.InstancePublicIPAddress}}`, for the instance's public IP address
 * `{{.ExistingRDataValue [set] [record]}}`, to get the existing RDATA
   on a resource record set. This function operates on the existing
   change set, operating on the specific fields of the resource record set
   asked for. This means that whether or not a properly rendered `Name`
   field depends on where this function is called - if called too early
   on a field that has not been iterated on yet that contains a templated
   field, the data will be incomplete. Lookups that result in no data
   returned, an out of range value index, or a Route 53 API error will
   cause an error.

### Note on terminating instances

Note that on termination events, IP address values will be rendered as
empty strings, so take care when using DELETE events that you don't
attempt to delete a non-existent, or even worse, an incorrect, record.
Use `ExistingRDataValue` to locate the existing resource record for the
value instead (as explained in the main example).

## License

```
Copyright 2016 PayByPhone Technologies, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```