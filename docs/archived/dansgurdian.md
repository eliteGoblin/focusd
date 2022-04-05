


```
sudo apt-get install clamav-freshclam iptables dansguardian squid

sudo sed -i 's/http_port 3128/http_port 3128 transparent/g' /etc/squid/squid.conf

sudo sed -i 's/#                always_direct allow local-servers/always_direct allow all/g' /etc/squid/squid.conf

sed -i "s%accessdeniedaddress = 'http://YOURSERVER.YOURDOMAIN/cgi-bin/dansguardian.pl'%accessdeniedaddress = 'http://localhost/cgi-bin/dansguardian.pl'%g" /etc/dansguardian/dansguardian.conf
```