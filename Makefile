.PHONY: inspect-topic psql ws

KAFKA_TOPIC := chat-messages
SQL := SELECT VERSION();

inspect-topic:
	docker compose exec kafka \
		/opt/kafka/bin/kafka-console-consumer.sh \
		--bootstrap-server localhost:9092 \
		--topic $(KAFKA_TOPIC) \
		--from-beginning \
		--property print.headers=true

ws:
	wscat -c "ws://localhost:8080/ws?user_id=1"

psql:
	docker compose exec postgres \
		psql -U user -d chat_db -c '$(SQL)'
