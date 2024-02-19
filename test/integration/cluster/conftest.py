import pytest
from helpers import EtcdInstance


# ######## #
# Fixtures #
# ######## #
@pytest.fixture(scope="session")
def etcd_session(request, session_tmpdir):
    tmpdir = session_tmpdir
    host = "localhost"
    port = 12388
    etcd_instance = EtcdInstance(host, port, tmpdir)
    etcd_instance.start()

    request.addfinalizer(lambda: etcd_instance.stop())
    return etcd_instance


@pytest.fixture(scope="function")
def etcd(etcd_session):
    etcd_session.truncate()
    return etcd_session
