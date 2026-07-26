.PHONY: build test lint fmt install clean demo

build:
	go build -o tf-upgrade-advisor .

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	gofmt -l . && go vet ./...

install:
	go install .

# Runs the README example against the checked-in fixture. Exits 1 by design.
demo: build
	./tf-upgrade-advisor scan --provider aws --from 5 --to 6 --format text testdata/aws5 || true

clean:
	rm -f tf-upgrade-advisor
