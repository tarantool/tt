package = 'sharded_app'
version = 'scm-1'
source  = {
    url = '/dev/null',
}
dependencies = {
    -- 0.1.42 is the first release shipping the recovery point manager
    -- vshard-router backend, which the backup tests rely on.
    'vshard == 0.1.42'
}
build = {
    type = 'none';
}
