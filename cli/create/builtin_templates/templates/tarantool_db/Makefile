SHELL := /bin/bash

up:
	docker network create tarantooldb_network
	cd tools \
		&& docker compose up -d --build \
		&& docker compose exec -it tcm-1 bash -c "\
			tools/client/wait_etcd_ready.sh 15 http://etcd:2379 \
			&& tt cluster publish http://etcd:2379/tdb config.yml --force \
			&& tools/client/wait_config_ready.sh 25 http://etcd:2379/tdb \
			&& echo Load config done"
	cd cluster && docker compose up -d --build
	echo 'All services configured'

down:
	cd cluster && docker compose down
	cd tools && docker compose down
	docker container prune -f
	docker network prune -f

restart: down up

migrate:
	docker compose -f cluster/docker-compose.yml exec -it tarantool-router-msk bash -c "\
		tt migrations publish http://etcd:2379/tdb migrations \
	"
	docker compose -f cluster/docker-compose.yml exec -it tarantool-router-msk bash -c "\
		tt migrations apply http://etcd:2379/tdb \
		--tarantool-username=admin \
		--tarantool-password=secret-cluster-cookie \
	"
	docker compose -f cluster/docker-compose.yml exec -it tarantool-router-msk bash -c "\
		tt migrations status http://etcd:2379/tdb \
		--tarantool-username=admin \
		--tarantool-password=secret-cluster-cookie \
	"

.PHONY: test
test: migrate
	pytest -v test
