// Code generated by paths_generate.lua; DO NOT EDIT.
//
// To update the file:
// 1. Install a latest Tarantool version.
// 2. Run: go generate ./...

package cluster

var ConfigEnvPaths = [][]string{
	[]string{"app", "cfg"},
	[]string{"app", "file"},
	[]string{"app", "module"},
	[]string{"audit_log", "file"},
	[]string{"audit_log", "filter"},
	[]string{"audit_log", "format"},
	[]string{"audit_log", "nonblock"},
	[]string{"audit_log", "pipe"},
	[]string{"audit_log", "syslog", "facility"},
	[]string{"audit_log", "syslog", "identity"},
	[]string{"audit_log", "syslog", "server"},
	[]string{"audit_log", "to"},
	[]string{"config", "etcd", "endpoints"},
	[]string{"config", "etcd", "http", "request", "timeout"},
	[]string{"config", "etcd", "http", "request", "unix_socket"},
	[]string{"config", "etcd", "password"},
	[]string{"config", "etcd", "prefix"},
	[]string{"config", "etcd", "ssl", "ca_file"},
	[]string{"config", "etcd", "ssl", "ca_path"},
	[]string{"config", "etcd", "ssl", "ssl_key"},
	[]string{"config", "etcd", "ssl", "verify_host"},
	[]string{"config", "etcd", "ssl", "verify_peer"},
	[]string{"config", "etcd", "username"},
	[]string{"config", "reload"},
	[]string{"config", "version"},
	[]string{"console", "enabled"},
	[]string{"console", "socket"},
	[]string{"credentials", "roles"},
	[]string{"credentials", "users"},
	[]string{"database", "hot_standby"},
	[]string{"database", "instance_uuid"},
	[]string{"database", "mode"},
	[]string{"database", "replicaset_uuid"},
	[]string{"database", "txn_isolation"},
	[]string{"database", "txn_timeout"},
	[]string{"database", "use_mvcc_engine"},
	[]string{"feedback", "crashinfo"},
	[]string{"feedback", "enabled"},
	[]string{"feedback", "host"},
	[]string{"feedback", "interval"},
	[]string{"feedback", "metrics_collect_interval"},
	[]string{"feedback", "metrics_limit"},
	[]string{"feedback", "send_metrics"},
	[]string{"fiber", "io_collect_interval"},
	[]string{"fiber", "slice", "err"},
	[]string{"fiber", "slice", "warn"},
	[]string{"fiber", "too_long_threshold"},
	[]string{"fiber", "top", "enabled"},
	[]string{"fiber", "worker_pool_threads"},
	[]string{"flightrec", "enabled"},
	[]string{"flightrec", "logs_log_level"},
	[]string{"flightrec", "logs_max_msg_size"},
	[]string{"flightrec", "logs_size"},
	[]string{"flightrec", "metrics_interval"},
	[]string{"flightrec", "metrics_period"},
	[]string{"flightrec", "requests_max_req_size"},
	[]string{"flightrec", "requests_max_res_size"},
	[]string{"flightrec", "requests_size"},
	[]string{"iproto", "advertise", "client"},
	[]string{"iproto", "advertise", "peer"},
	[]string{"iproto", "advertise", "sharding"},
	[]string{"iproto", "listen"},
	[]string{"iproto", "net_msg_max"},
	[]string{"iproto", "readahead"},
	[]string{"iproto", "threads"},
	[]string{"log", "file"},
	[]string{"log", "format"},
	[]string{"log", "level"},
	[]string{"log", "modules"},
	[]string{"log", "nonblock"},
	[]string{"log", "pipe"},
	[]string{"log", "syslog", "facility"},
	[]string{"log", "syslog", "identity"},
	[]string{"log", "syslog", "server"},
	[]string{"log", "to"},
	[]string{"memtx", "allocator"},
	[]string{"memtx", "max_tuple_size"},
	[]string{"memtx", "memory"},
	[]string{"memtx", "min_tuple_size"},
	[]string{"memtx", "slab_alloc_factor"},
	[]string{"memtx", "slab_alloc_granularity"},
	[]string{"memtx", "sort_threads"},
	[]string{"metrics", "exclude"},
	[]string{"metrics", "include"},
	[]string{"metrics", "labels"},
	[]string{"process", "background"},
	[]string{"process", "coredump"},
	[]string{"process", "pid_file"},
	[]string{"process", "strip_core"},
	[]string{"process", "title"},
	[]string{"process", "username"},
	[]string{"process", "work_dir"},
	[]string{"replication", "anon"},
	[]string{"replication", "bootstrap_strategy"},
	[]string{"replication", "connect_timeout"},
	[]string{"replication", "election_fencing_mode"},
	[]string{"replication", "election_mode"},
	[]string{"replication", "election_timeout"},
	[]string{"replication", "failover"},
	[]string{"replication", "peers"},
	[]string{"replication", "skip_conflict"},
	[]string{"replication", "sync_lag"},
	[]string{"replication", "sync_timeout"},
	[]string{"replication", "synchro_quorum"},
	[]string{"replication", "synchro_timeout"},
	[]string{"replication", "threads"},
	[]string{"replication", "timeout"},
	[]string{"roles"},
	[]string{"roles_cfg"},
	[]string{"security", "auth_delay"},
	[]string{"security", "auth_retries"},
	[]string{"security", "auth_type"},
	[]string{"security", "disable_guest"},
	[]string{"security", "password_enforce_digits"},
	[]string{"security", "password_enforce_lowercase"},
	[]string{"security", "password_enforce_specialchars"},
	[]string{"security", "password_enforce_uppercase"},
	[]string{"security", "password_history_length"},
	[]string{"security", "password_lifetime_days"},
	[]string{"security", "password_min_length"},
	[]string{"sharding", "bucket_count"},
	[]string{"sharding", "connection_outdate_delay"},
	[]string{"sharding", "discovery_mode"},
	[]string{"sharding", "failover_ping_timeout"},
	[]string{"sharding", "lock"},
	[]string{"sharding", "rebalancer_disbalance_threshold"},
	[]string{"sharding", "rebalancer_max_receiving"},
	[]string{"sharding", "rebalancer_max_sending"},
	[]string{"sharding", "roles"},
	[]string{"sharding", "sched_move_quota"},
	[]string{"sharding", "sched_ref_quota"},
	[]string{"sharding", "shard_index"},
	[]string{"sharding", "sync_timeout"},
	[]string{"sharding", "zone"},
	[]string{"snapshot", "by", "interval"},
	[]string{"snapshot", "by", "wal_size"},
	[]string{"snapshot", "count"},
	[]string{"snapshot", "dir"},
	[]string{"snapshot", "snap_io_rate_limit"},
	[]string{"sql", "cache_size"},
	[]string{"vinyl", "bloom_fpr"},
	[]string{"vinyl", "cache"},
	[]string{"vinyl", "defer_deletes"},
	[]string{"vinyl", "dir"},
	[]string{"vinyl", "max_tuple_size"},
	[]string{"vinyl", "memory"},
	[]string{"vinyl", "page_size"},
	[]string{"vinyl", "range_size"},
	[]string{"vinyl", "read_threads"},
	[]string{"vinyl", "run_count_per_level"},
	[]string{"vinyl", "run_size_ratio"},
	[]string{"vinyl", "timeout"},
	[]string{"vinyl", "write_threads"},
	[]string{"wal", "cleanup_delay"},
	[]string{"wal", "dir"},
	[]string{"wal", "dir_rescan_delay"},
	[]string{"wal", "ext", "new"},
	[]string{"wal", "ext", "old"},
	[]string{"wal", "ext", "spaces"},
	[]string{"wal", "max_size"},
	[]string{"wal", "mode"},
	[]string{"wal", "queue_max_size"},
}

var TarantoolSchema = []SchemaPath{
	SchemaPath{
		Path: []string{"app", "cfg"},
		Validator: MakeMapValidator(
			StringValidator{},
			AnyValidator{}),
	},
	SchemaPath{
		Path:      []string{"app", "file"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"app", "module"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"audit_log", "file"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"audit_log", "filter"},
		Validator: MakeArrayValidator(
			MakeAllowedValidator(
				StringValidator{},
				[]any{
					"audit_enable",
					"custom",
					"auth_ok",
					"auth_fail",
					"disconnect",
					"user_create",
					"user_drop",
					"role_create",
					"role_drop",
					"user_enable",
					"user_disable",
					"user_grant_rights",
					"user_revoke_rights",
					"role_grant_rights",
					"role_revoke_rights",
					"password_change",
					"access_denied",
					"eval",
					"call",
					"space_select",
					"space_create",
					"space_alter",
					"space_drop",
					"space_insert",
					"space_replace",
					"space_delete",
					"none",
					"all",
					"audit",
					"auth",
					"priv",
					"ddl",
					"dml",
					"data_operations",
					"compatibility",
				})),
	},
	SchemaPath{
		Path: []string{"audit_log", "format"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"plain",
				"json",
				"csv",
			}),
	},
	SchemaPath{
		Path:      []string{"audit_log", "nonblock"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"audit_log", "pipe"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"audit_log", "syslog", "facility"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"audit_log", "syslog", "identity"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"audit_log", "syslog", "server"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"audit_log", "to"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"devnull",
				"file",
				"pipe",
				"syslog",
			}),
	},
	SchemaPath{
		Path: []string{"config", "etcd", "endpoints"},
		Validator: MakeArrayValidator(
			StringValidator{}),
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "http", "request", "timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "http", "request", "unix_socket"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "password"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "prefix"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "ssl", "ca_file"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "ssl", "ca_path"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "ssl", "ssl_key"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "ssl", "verify_host"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "ssl", "verify_peer"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"config", "etcd", "username"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"config", "reload"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"auto",
				"manual",
			}),
	},
	SchemaPath{
		Path: []string{"config", "version"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"dev",
			}),
	},
	SchemaPath{
		Path:      []string{"console", "enabled"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"console", "socket"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"credentials", "roles"},
		Validator: MakeMapValidator(
			StringValidator{},
			MakeRecordValidator(map[string]Validator{
				"privileges": MakeArrayValidator(
					MakeRecordValidator(map[string]Validator{
						"permissions": MakeArrayValidator(
							MakeAllowedValidator(
								StringValidator{},
								[]any{
									"read",
									"write",
									"execute",
									"create",
									"alter",
									"drop",
									"usage",
									"session",
								})),
						"universe": BooleanValidator{},
					})),
				"roles": MakeArrayValidator(
					StringValidator{}),
			})),
	},
	SchemaPath{
		Path: []string{"credentials", "users"},
		Validator: MakeMapValidator(
			StringValidator{},
			MakeRecordValidator(map[string]Validator{
				"password": StringValidator{},
				"roles": MakeArrayValidator(
					StringValidator{}),
				"privileges": MakeArrayValidator(
					MakeRecordValidator(map[string]Validator{
						"permissions": MakeArrayValidator(
							MakeAllowedValidator(
								StringValidator{},
								[]any{
									"read",
									"write",
									"execute",
									"create",
									"alter",
									"drop",
									"usage",
									"session",
								})),
						"universe": BooleanValidator{},
					})),
			})),
	},
	SchemaPath{
		Path:      []string{"database", "hot_standby"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"database", "instance_uuid"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"database", "mode"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"ro",
				"rw",
			}),
	},
	SchemaPath{
		Path:      []string{"database", "replicaset_uuid"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"database", "txn_isolation"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"read-committed",
				"read-confirmed",
				"best-effort",
			}),
	},
	SchemaPath{
		Path:      []string{"database", "txn_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"database", "use_mvcc_engine"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "crashinfo"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "enabled"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "host"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "interval"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "metrics_collect_interval"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "metrics_limit"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"feedback", "send_metrics"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"fiber", "io_collect_interval"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"fiber", "slice", "err"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"fiber", "slice", "warn"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"fiber", "too_long_threshold"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"fiber", "top", "enabled"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"fiber", "worker_pool_threads"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "enabled"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path: []string{"flightrec", "logs_log_level"},
		Validator: MakeAllowedValidator(
			IntegerValidator{},
			[]any{
				0,
				1,
				2,
				3,
				4,
				5,
				6,
				7,
			}),
	},
	SchemaPath{
		Path:      []string{"flightrec", "logs_max_msg_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "logs_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "metrics_interval"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "metrics_period"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "requests_max_req_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "requests_max_res_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"flightrec", "requests_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "advertise", "client"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "advertise", "peer"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "advertise", "sharding"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "listen"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "net_msg_max"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "readahead"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"iproto", "threads"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"log", "file"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"log", "format"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"plain",
				"json",
			}),
	},
	SchemaPath{
		Path: []string{"log", "level"},
		Validator: MakeAllowedValidator(
			MakeSequenceValidator(NumberValidator{}, StringValidator{}),
			[]any{
				0,
				"fatal",
				1,
				"syserror",
				2,
				"error",
				3,
				"crit",
				4,
				"warn",
				5,
				"info",
				6,
				"verbose",
				7,
				"debug",
			}),
	},
	SchemaPath{
		Path: []string{"log", "modules"},
		Validator: MakeMapValidator(
			StringValidator{},
			MakeSequenceValidator(NumberValidator{}, StringValidator{})),
	},
	SchemaPath{
		Path:      []string{"log", "nonblock"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"log", "pipe"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"log", "syslog", "facility"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"log", "syslog", "identity"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"log", "syslog", "server"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path: []string{"log", "to"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"stderr",
				"file",
				"pipe",
				"syslog",
			}),
	},
	SchemaPath{
		Path: []string{"memtx", "allocator"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"small",
				"system",
			}),
	},
	SchemaPath{
		Path:      []string{"memtx", "max_tuple_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"memtx", "memory"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"memtx", "min_tuple_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"memtx", "slab_alloc_factor"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"memtx", "slab_alloc_granularity"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"memtx", "sort_threads"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path: []string{"metrics", "exclude"},
		Validator: MakeArrayValidator(
			MakeAllowedValidator(
				StringValidator{},
				[]any{
					"all",
					"network",
					"operations",
					"system",
					"replicas",
					"info",
					"slab",
					"runtime",
					"memory",
					"spaces",
					"fibers",
					"cpu",
					"vinyl",
					"memtx",
					"luajit",
					"clock",
					"event_loop",
				})),
	},
	SchemaPath{
		Path: []string{"metrics", "include"},
		Validator: MakeArrayValidator(
			MakeAllowedValidator(
				StringValidator{},
				[]any{
					"all",
					"network",
					"operations",
					"system",
					"replicas",
					"info",
					"slab",
					"runtime",
					"memory",
					"spaces",
					"fibers",
					"cpu",
					"vinyl",
					"memtx",
					"luajit",
					"clock",
					"event_loop",
				})),
	},
	SchemaPath{
		Path: []string{"metrics", "labels"},
		Validator: MakeMapValidator(
			StringValidator{},
			StringValidator{}),
	},
	SchemaPath{
		Path:      []string{"process", "background"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"process", "coredump"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"process", "pid_file"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"process", "strip_core"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"process", "title"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"process", "username"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"process", "work_dir"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"replication", "anon"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path: []string{"replication", "bootstrap_strategy"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"auto",
				"config",
				"supervised",
				"legacy",
			}),
	},
	SchemaPath{
		Path:      []string{"replication", "connect_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path: []string{"replication", "election_fencing_mode"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"off",
				"soft",
				"strict",
			}),
	},
	SchemaPath{
		Path: []string{"replication", "election_mode"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"off",
				"voter",
				"manual",
				"candidate",
			}),
	},
	SchemaPath{
		Path:      []string{"replication", "election_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path: []string{"replication", "failover"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"off",
				"manual",
				"election",
			}),
	},
	SchemaPath{
		Path: []string{"replication", "peers"},
		Validator: MakeArrayValidator(
			StringValidator{}),
	},
	SchemaPath{
		Path:      []string{"replication", "skip_conflict"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"replication", "sync_lag"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"replication", "sync_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"replication", "synchro_quorum"},
		Validator: MakeSequenceValidator(NumberValidator{}, StringValidator{}),
	},
	SchemaPath{
		Path:      []string{"replication", "synchro_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"replication", "threads"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"replication", "timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path: []string{"roles"},
		Validator: MakeArrayValidator(
			StringValidator{}),
	},
	SchemaPath{
		Path: []string{"roles_cfg"},
		Validator: MakeMapValidator(
			StringValidator{},
			AnyValidator{}),
	},
	SchemaPath{
		Path:      []string{"security", "auth_delay"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "auth_retries"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path: []string{"security", "auth_type"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"chap-sha1",
				"pap-sha256",
			}),
	},
	SchemaPath{
		Path:      []string{"security", "disable_guest"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_enforce_digits"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_enforce_lowercase"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_enforce_specialchars"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_enforce_uppercase"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_history_length"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_lifetime_days"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"security", "password_min_length"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "bucket_count"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "connection_outdate_delay"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path: []string{"sharding", "discovery_mode"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"on",
				"off",
				"once",
			}),
	},
	SchemaPath{
		Path:      []string{"sharding", "failover_ping_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "lock"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "rebalancer_disbalance_threshold"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "rebalancer_max_receiving"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "rebalancer_max_sending"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path: []string{"sharding", "roles"},
		Validator: MakeArrayValidator(
			MakeAllowedValidator(
				StringValidator{},
				[]any{
					"router",
					"storage",
				})),
	},
	SchemaPath{
		Path:      []string{"sharding", "sched_move_quota"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "sched_ref_quota"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "shard_index"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "sync_timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"sharding", "zone"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"snapshot", "by", "interval"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"snapshot", "by", "wal_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"snapshot", "count"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"snapshot", "dir"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"snapshot", "snap_io_rate_limit"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"sql", "cache_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "bloom_fpr"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "cache"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "defer_deletes"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "dir"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "max_tuple_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "memory"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "page_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "range_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "read_threads"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "run_count_per_level"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "run_size_ratio"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "timeout"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"vinyl", "write_threads"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path:      []string{"wal", "cleanup_delay"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"wal", "dir"},
		Validator: StringValidator{},
	},
	SchemaPath{
		Path:      []string{"wal", "dir_rescan_delay"},
		Validator: NumberValidator{},
	},
	SchemaPath{
		Path:      []string{"wal", "ext", "new"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path:      []string{"wal", "ext", "old"},
		Validator: BooleanValidator{},
	},
	SchemaPath{
		Path: []string{"wal", "ext", "spaces"},
		Validator: MakeMapValidator(
			StringValidator{},
			MakeRecordValidator(map[string]Validator{
				"old": BooleanValidator{},
				"new": BooleanValidator{},
			})),
	},
	SchemaPath{
		Path:      []string{"wal", "max_size"},
		Validator: IntegerValidator{},
	},
	SchemaPath{
		Path: []string{"wal", "mode"},
		Validator: MakeAllowedValidator(
			StringValidator{},
			[]any{
				"none",
				"write",
				"fsync",
			}),
	},
	SchemaPath{
		Path:      []string{"wal", "queue_max_size"},
		Validator: IntegerValidator{},
	},
}
