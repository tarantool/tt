package = 'app1'
version = 'scm-1'
source  = {
    url = '/dev/null',
}
-- Put any modules your app depends on here.
dependencies = {
    'metrics == 0.13.0-1',
}
build = {
    type = 'none';
}
