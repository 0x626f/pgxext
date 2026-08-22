set shell := ["bash", "-euo", "pipefail", "-c"]

# Short alias for the complete integration matrix.
test: test-integration

# Run every package on PostgreSQL 16, 17, and 18 with compatible TimescaleDB versions.
test-integration:
    #!/usr/bin/env bash
    set -euo pipefail

    coverage_dir="${PGXEXT_COVERAGE_DIR:-.coverage}"
    mkdir -p "$coverage_dir"
    race_flag=()
    if [[ "${PGXEXT_RACE:-0}" == "1" ]]; then
        race_flag=(-race)
    fi

    containers=()
    cleanup() {
        for container in "${containers[@]}"; do
            docker rm --force "$container" >/dev/null 2>&1 || true
        done
        containers=()
    }

    wait_for_postgres() {
        local container="$1"
        local attempt
        for ((attempt = 1; attempt <= 60; attempt++)); do
            if docker exec "$container" pg_isready -U postgres -d pgxext >/dev/null 2>&1; then
                return 0
            fi
            if ! docker container inspect "$container" >/dev/null 2>&1; then
                printf 'Container %s stopped before becoming ready\n' "$container" >&2
                docker logs "$container" >&2 || true
                return 1
            fi
            sleep 1
        done
        printf 'Timed out waiting for %s\n' "$container" >&2
        docker logs "$container" >&2 || true
        return 1
    }

    published_port() {
        local mapping
        mapping="$(docker port "$1" 5432/tcp)"
        printf '%s\n' "${mapping##*:}"
    }
    trap cleanup EXIT

    matrix=(
        "16|postgres:16|timescale/timescaledb:2.20.2-pg16|2.20.2"
        "17|postgres:17|timescale/timescaledb:2.20.2-pg17|2.20.2"
        "18|postgres:18|timescale/timescaledb:2.23.1-pg18|2.23.1"
    )

    for target in "${matrix[@]}"; do
        IFS='|' read -r postgres_major postgres_image timescale_image timescale_version <<<"$target"
        printf '\nTesting PostgreSQL %s with TimescaleDB %s\n' "$postgres_major" "$timescale_version"

        cleanup
        postgres_container="pgxext-postgres-${postgres_major}-$$"
        timescale_container="pgxext-timescaledb-${postgres_major}-$$"
        containers+=("$postgres_container" "$timescale_container")

        docker run --detach --rm \
            --name "$postgres_container" \
            --env POSTGRES_DB=pgxext \
            --env POSTGRES_USER=postgres \
            --env POSTGRES_PASSWORD=postgres \
            --publish 127.0.0.1::5432 \
            "$postgres_image" >/dev/null

        docker run --detach --rm \
            --name "$timescale_container" \
            --env POSTGRES_DB=pgxext \
            --env POSTGRES_USER=postgres \
            --env POSTGRES_PASSWORD=postgres \
            --publish 127.0.0.1::5432 \
            "$timescale_image" >/dev/null

        wait_for_postgres "$postgres_container"
        wait_for_postgres "$timescale_container"
        postgres_port="$(published_port "$postgres_container")"
        timescale_port="$(published_port "$timescale_container")"

        coverage_profile="${coverage_dir}/postgres-${postgres_major}-timescaledb-${timescale_version}.coverprofile"

        PGXEXT_REQUIRE_INTEGRATION=1 \
        TEST_DATABASE_URL="postgres://postgres:postgres@localhost:${postgres_port}/pgxext?sslmode=disable" \
        TEST_POSTGRES_MAJOR="$postgres_major" \
        TEST_TIMESCALE_DATABASE_URL="postgres://postgres:postgres@localhost:${timescale_port}/pgxext?sslmode=disable" \
        TEST_TIMESCALE_POSTGRES_MAJOR="$postgres_major" \
        TEST_TIMESCALE_EXTENSION_VERSION="$timescale_version" \
            go test -count=1 "${race_flag[@]}" -covermode=atomic -coverprofile="$coverage_profile" ./...

        printf 'Coverage profile: %s\n' "$coverage_profile"
        go tool cover -func="$coverage_profile" | tail -n 1

        cleanup
    done
