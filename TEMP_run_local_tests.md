# build image 
sudo docker build -t tt_test .
# run
sudo docker run -it --name tt_test_run tt_test /bin/bash
container cmd:
mage integration

# delete tmp
sudo docker rm tt_test_run 
sudo docker rmi tt_test