FROM docker:dind

WORKDIR /app 

RUN apk add --no-cache bash python3 py3-pip

RUN pip install aws-sam-cli==1.150.1 --break-system-packages && \
    python3 -c 'from samcli.cli.main import cli; assert callable(cli)'
ADD aws-sam-build/release/linux/amd64/plugin /bin/


ENTRYPOINT ["/bin/plugin"]
