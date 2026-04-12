.PHONY: install test vet fmt check

install:
	cd ata && go install ./...

test:
	cd ata && go test ./...

vet:
	cd ata && go vet ./...

fmt:
	gofmt -l .

check: vet test
