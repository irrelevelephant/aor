.PHONY: install test vet fmt check

install:
	go install .
	go install ./ata/...

test:
	go test ./...
	cd ata && go test ./...

vet:
	go vet ./...
	cd ata && go vet ./...

fmt:
	gofmt -l .

check: vet test
