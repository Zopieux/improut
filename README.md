# improut ⋅ dead simple image hosting

This server is compatible with a subset of the 
[Lutim API](https://framagit.org/fiat-tux/hat-softwares/lutim/-/wikis/API), 
so apps like the open-source [Gotim](http://www.gobl.im/) can be used to upload
image from mobile phones.

## Building

    $ go build
    
## Testing

    $ go test

## Running under Nginx

There is a demo [`docker-compose.yml`](demo/docker-compose.yml) to showcase
Nginx integration, with `X-Accel-Redirect` support and `auth_basic`-protected 
upload endpoint.

    $ ./improut -xaccel /internal -root $PWD/demo/filestore
    $ chmod +rx $PWD/demo/filestore && cd demo && docker-compose up    
