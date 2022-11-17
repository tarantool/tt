local workdir = os.getenv('TEST_WORKDIR')
default_cfg = {
    pid_file   = workdir,
    wal_dir    = workdir,
    snap_dir   = workdir,
    vinyl_dir  = workdir,
    logger     = workdir,
    background = false,
}

instance_dir = workdir

