# Running integration tests in a containerized environment

The final image `tt_integration_test` includes all the necessary components to run the test. At the moment, only `mage integration` is functional.
1. Create a base image that contains all necessary infrastructure (Tarantool, Etcd, etc.). `docker build -f ./test/docker/Dockerfile_base -t tt_integration_test_base .` The default Tarantool version is 3.0.2. If you need a different version, use the `--build-arg` flag to specify the desired version. Example: `docker build --build-arg TARANTOOL_VERSION=3.8.0 -f ./test/docker/Dockerfile_base -t tt_integration_test_base .`.
2. Create an image that contains the current state of TT source files. This image is used for testing. Example: `docker build -f ./test/docker/Dockerfile -t tt_integration_test .`.
3. Create a subnet for the tests, as the tests require IPv6. Example: `sudo docker build -f ./test/docker/Dockerfile -t tt_integration_test .`.
4. Launch the test container with the following command: `docker run -it --security-opt seccomp=unconfined  --network my-ipv6-network --name tt_test_run tt_integration_test /bin/bash`.
The `seccomp=unconfined` option is used to execute the unshare system call in the tests. 
5) Run the `mage integration` test in container terminal.