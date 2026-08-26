IMAGE ?= phraseup:latest
PROJECT ?= experimental-apps-v2
REGION ?= us-west1

.PHONY: test build run deploy clean

test:
	go test ./...

build:
	docker build -t $(IMAGE) .

# Run the real deploy artifact locally. Needs `apps-platform connect-db` (or
# any local Postgres reachable as host=localhost) and DEV_USER_EMAIL to act
# as an authenticated user without IAP.
run: build
	docker run --rm -p 8080:8080 \
		-e DEV_USER_EMAIL=$${DEV_USER_EMAIL:-you@applied.co} \
		--network host \
		$(IMAGE)

deploy: build
	apps-platform app deploy --image $(IMAGE)

clean:
	-docker image rm $(IMAGE) 2>/dev/null || true
