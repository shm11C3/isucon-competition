#!/usr/bin/env python3
"""Provision one control EC2 with AWS CLI; never modify existing practice hosts."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--config', default=str(ROOT / 'config/infra.json'))
    p.add_argument('--create', action='store_true', help='explicitly create the resources shown by this configuration')
    args = p.parse_args()
    config = json.loads(Path(args.config).read_text())
    base = ['aws', '--region', config['region'], '--no-cli-pager']

    def aws(*arguments, allow_missing=False):
        result = subprocess.run([*base, *arguments, '--output', 'json'], capture_output=True, text=True)
        if result.returncode:
            if allow_missing and ('NoSuchEntity' in result.stderr or 'InvalidGroup.NotFound' in result.stderr):
                return None
            raise RuntimeError(result.stderr.strip())
        return json.loads(result.stdout) if result.stdout.strip() else {}

    try:
        identity = aws('sts', 'get-caller-identity')
        if identity['Account'] != config['account_id']:
            raise ValueError('AWS account differs from config/infra.json; refusing provisioning')
        if not args.create:
            print(json.dumps({'plan': config, 'inbound': [], 'access': 'SSM', 'ebs_encrypted': True,
                              'cpu_credits': 'standard', 'public_ip_for_outbound': True}, indent=2))
            return 0
        name = config['project'] + '-control'
        tags = [{'Key': 'Project', 'Value': config['project']},
                {'Key': 'Role', 'Value': 'control'}, {'Key': 'Name', 'Value': name}]
        role_name = name + '-ssm'
        existing = aws('ec2', 'describe-instances', '--filters',
                       'Name=tag:Project,Values=' + config['project'], 'Name=tag:Role,Values=control',
                       'Name=instance-state-name,Values=pending,running,stopping,stopped')
        instances = [i for r in existing['Reservations'] for i in r['Instances']]
        if len(instances) > 1:
            raise ValueError('multiple control hosts found; refusing to choose or create another')
        if instances:
            instance = instances[0]
        else:
            subnet = aws('ec2', 'describe-subnets', '--subnet-ids', config['subnet_id'])['Subnets'][0]
            if subnet['VpcId'] != config['vpc_id']:
                raise ValueError('subnet is not in the configured VPC')
            role = aws('iam', 'get-role', '--role-name', role_name, allow_missing=True)
            trust = {'Version': '2012-10-17', 'Statement': [{
                'Effect': 'Allow', 'Principal': {'Service': 'ec2.amazonaws.com'}, 'Action': 'sts:AssumeRole'}]}
            if role is None:
                aws('iam', 'create-role', '--role-name', role_name,
                    '--assume-role-policy-document', json.dumps(trust), '--tags', json.dumps(tags))
            else:
                actual_tags = {t['Key']: t['Value'] for t in role['Role'].get('Tags', [])}
                if actual_tags.get('Project') != config['project']:
                    raise ValueError('same-named IAM role has another owner; refusing to modify it')
            aws('iam', 'attach-role-policy', '--role-name', role_name, '--policy-arn',
                'arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore')
            profile = aws('iam', 'get-instance-profile', '--instance-profile-name', role_name, allow_missing=True)
            if profile is None:
                aws('iam', 'create-instance-profile', '--instance-profile-name', role_name, '--tags', json.dumps(tags))
                aws('iam', 'add-role-to-instance-profile', '--instance-profile-name', role_name, '--role-name', role_name)
                time.sleep(10)
            elif [r['RoleName'] for r in profile['InstanceProfile']['Roles']] != [role_name]:
                raise ValueError('same-named instance profile has unexpected roles')
            groups = aws('ec2', 'describe-security-groups', '--filters',
                         'Name=group-name,Values=' + name, 'Name=vpc-id,Values=' + config['vpc_id'])['SecurityGroups']
            if groups:
                group = groups[0]
                actual_tags = {t['Key']: t['Value'] for t in group.get('Tags', [])}
                if actual_tags.get('Project') != config['project'] or group['IpPermissions']:
                    raise ValueError('same-named security group has unexpected ownership or inbound rules')
                group_id = group['GroupId']
            else:
                group_id = aws('ec2', 'create-security-group', '--group-name', name,
                               '--description', 'ISUCON control host; SSM only; no inbound rules',
                               '--vpc-id', config['vpc_id'], '--tag-specifications',
                               json.dumps([{'ResourceType': 'security-group', 'Tags': tags}]))['GroupId']
            ami = aws('ssm', 'get-parameter', '--name', config['ami_parameter'])['Parameter']['Value']
            image = aws('ec2', 'describe-images', '--image-ids', ami)['Images'][0]
            if image['OwnerId'] != '137112412989' or image['Architecture'] != 'x86_64':
                raise ValueError('AMI is not the expected AWS-owned x86_64 Amazon Linux image')
            local = ROOT / '.local'
            local.mkdir(exist_ok=True)
            token_file = local / 'ec2-client-token'
            if not token_file.exists():
                token_file.write_text(uuid.uuid4().hex)
            response = aws('ec2', 'run-instances', '--image-id', ami,
                           '--instance-type', config['instance_type'], '--count', '1',
                           '--client-token', token_file.read_text().strip(),
                           '--iam-instance-profile', 'Name=' + role_name,
                           '--network-interfaces', json.dumps([{'DeviceIndex': 0, 'SubnetId': config['subnet_id'],
                                                               'Groups': [group_id], 'AssociatePublicIpAddress': True,
                                                               'DeleteOnTermination': True}]),
                           '--metadata-options', 'HttpTokens=required,HttpEndpoint=enabled,HttpPutResponseHopLimit=1',
                           '--credit-specification', 'CpuCredits=standard',
                           '--block-device-mappings', json.dumps([{'DeviceName': image['RootDeviceName'], 'Ebs': {
                               'VolumeSize': config['volume_gib'], 'VolumeType': 'gp3',
                               'Encrypted': True, 'DeleteOnTermination': True}}]),
                           '--tag-specifications', json.dumps([
                               {'ResourceType': 'instance', 'Tags': tags}, {'ResourceType': 'volume', 'Tags': tags}]),
                           '--user-data', 'file://' + str(ROOT / 'infra/bootstrap.sh'))
            instance = response['Instances'][0]
        result = {'account_id': config['account_id'], 'region': config['region'],
                  'instance_id': instance['InstanceId'], 'instance_type': instance['InstanceType'],
                  'vpc_id': instance['VpcId'], 'subnet_id': instance['SubnetId'],
                  'role_name': role_name, 'project': config['project'],
                  'repository': 'shm11C3/isucon-competition', 'access': 'ssm',
                  'target_configured': False}
        (ROOT / 'config/control-host.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
        return 0
    except (ValueError, RuntimeError, KeyError, OSError) as error:
        print(str(error), file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
