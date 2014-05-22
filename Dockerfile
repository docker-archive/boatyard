FROM tutum/buildstep
RUN apt-get update
RUN apt-get install -y nginx supervisor
RUN echo "daemon off;" >> /etc/nginx/nginx.conf
RUN rm -f /etc/nginx/sites-enabled/*
ADD nginx.conf /etc/nginx/sites-enabled/builder.conf
ADD supervisord.conf /etc/supervisor/conf.d/supervisord-builder.conf

EXPOSE 80
CMD ["supervisord", "-c", "/etc/supervisor/supervisord.conf", "-n"]
