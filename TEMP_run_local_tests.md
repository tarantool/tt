# build image 
sudo docker build -t tt_test .
# run
docker network create --ipv6 --subnet fd01::/64 my-ipv6-network
sudo docker run -it --security-opt seccomp=unconfined  --network my-ipv6-network --name tt_test_run tt_test /bin/bash


container cmd:
mage integration

# delete tmp
sudo docker rm tt_test_run 
sudo docker rmi tt_test