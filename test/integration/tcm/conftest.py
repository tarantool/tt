import pytest


def pytest_addoption(parser):
    parser.addoption(
        "--update-testdata",
        action="store_true",
        default=False,
        help='Update "golden" test data files',
    )


@pytest.fixture(scope="session")
def update_testdata(request: pytest.FixtureRequest) -> bool:
    return bool(request.config.getoption("--update-testdata"))
