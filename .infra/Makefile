.PHONY: test-ansible-playbook

AWS_REGION := us-east-2
ROOT_DIR := $(dir $(realpath $(lastword $(MAKEFILE_LIST))))
TEST_CONTAINER_NAME := al2023-test

aws-spot-pricing:
	aws ec2 describe-spot-price-history \
		--region $(AWS_REGION) \
		--instance-types c8i.2xlarge \
		--product-descriptions "Linux/UNIX" \
		--max-items 5 \
		--query 'SpotPriceHistory[].{AZ:AvailabilityZone,Price:SpotPrice,Time:Timestamp}' \
		--output table

test-ansible-playbook:
	( \
		docker run --detach \
			--name $(TEST_CONTAINER_NAME) \
			--privileged \
			--cgroupns=host \
			--volume /sys/fs/cgroup:/sys/fs/cgroup:rw \
			--volume="$(ROOT_DIR)/ansible":/ansible:ro \
			geerlingguy/docker-amazonlinux2023-ansible:latest && \
		python -c "import time; time.sleep(5)" && \
		docker exec $(TEST_CONTAINER_NAME) /usr/local/bin/ansible-playbook \
			/ansible/local.yml \
			-i "localhost," \
			-c local -vvv && \
		docker rm -f $(TEST_CONTAINER_NAME) \
	) || ( \
		docker rm -f $(TEST_CONTAINER_NAME) \
	)
