# improut ⋅ dead simple image hosting

## Building

    $ go build
    
## Running under Nginx

There is a demo `docker-compose.yml` to showcase Nginx integration, with
X-Accel-Redirect support and auth_basic-protected upload endpoint.

    $ ./improut -xaccel /internal -root $PWD/demo/filestore
    $ chmod +rx $PWD/demo/filestore && cd demo && docker-compose up    
