FROM tt_integration_test_base
COPY . /tt

WORKDIR /tt
RUN git submodule update --init --recursive
ENV PATH="${PATH}:/root/go/bin:/go/bin:/tt"
RUN mage build
