.PHONY: kafka-describe-topic kafka-describe-group kafka-consume-messages psql redis-scan ws

KAFKA_TOPIC := chat-messages
KAFKA_GROUP := my-message-router
KAFKA_PARTITION := 0
KAFKA_OFFSET := 0
SQL := SELECT VERSION();

kafka-describe-topic:
	docker compose exec kafka \
		/opt/kafka/bin/kafka-topics.sh \
		--bootstrap-server localhost:9092 \
		--describe \
		--topic $(KAFKA_TOPIC)

kafka-describe-group:
	docker compose exec kafka \
		/opt/kafka/bin/kafka-consumer-groups.sh \
		--bootstrap-server localhost:9092 \
		--describe \
		--group $(KAFKA_GROUP)

kafka-get-message:
	docker compose exec kafka \
		/opt/kafka/bin/kafka-console-consumer.sh \
		--bootstrap-server localhost:9092 \
		--topic $(KAFKA_TOPIC) \
		--partition $(KAFKA_PARTITION) \
		--offset $(KAFKA_OFFSET) \
		--max-messages 1 \
		--property print.timestamp=true \
		--property print.offset=true \
		--property print.partition=true \
		--property print.key=true

kafka-consume-messages:
	docker compose exec kafka \
		/opt/kafka/bin/kafka-console-consumer.sh \
		--bootstrap-server localhost:9092 \
		--topic $(KAFKA_TOPIC) \
		--from-beginning \
		--property print.timestamp=true \
		--property print.headers=true

ws:
	wscat -c "ws://localhost:8080/ws?user_id=1"

psql:
	docker compose exec postgres \
		psql -U user -d chat_db -c '$(SQL)'

redis-scan:
	docker compose exec redis \
		redis-cli --scan
