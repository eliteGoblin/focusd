*  dashboard: https://dashboard.opendns.com/settings/241644332/content_filtering
*  探测是否在使用opendns: https://welcome.opendns.com/

## Family Shield

*  设置系统DNS为此DNS地址
    - 208.67.222.123
    - 208.67.220.123

ubuntu右上角, 网络，设置--> IPv4 --> Automatic选择情况下，输入上诉两个dns.

同时设置本机dns为dns server(貌似仅设置wifi的没用，还是会用本机的127.0.0.53, systemd resolve)

参考 [DNS on Ubuntu 18.04](https://datawookie.netlify.com/blog/2018/10/dns-on-ubuntu-18.04/)

```
sudo apt install resolvconf
vim /etc/resolvconf/resolv.conf.d/head 加入dns server 
sudo service resolvconf restart
```

验证 /etc/resolve.conf 是否修改过来了(这个文件不能手动修改，重启电脑会restore之前的)

访问https://welcome.opendns.com/应验证成功

[Family Shield](https://www.opendns.com/setupguide/#familyshield)

下面开始定制化自己的DNS: 

## Family Plan

*  注册账号，configure open dns 自动上报动态IP

[Configure IP updater for OpenDNS](https://askubuntu.com/questions/48568/configure-ip-updater-for-opendns)

官方文档: https://support.opendns.com/hc/en-us/articles/227987727-Linux-IP-Updater-for-Dynamic-Networks

*  用这个成功

安装ddclient

```
sudo apt-get install ddclient
```

```
# #
# # OpenDNS.com account-configuration
# #

protocol=dyndns2
use=web, web=myip.dnsomatic.com
ssl=yes
server=updates.opendns.com
login=elitegoblinrb@gmail.com
password=''
Office
```

最后的Office是network label的名字

```
Test if is all ok with the command:

# 测试
sudo ddclient -verbose -file /etc/ddclient.conf
```

正常会看到ip地址已经设置: 

```
SUCCESS:  updating Office: good: IP address set to 49.195.153.28
或者
SUCCESS:  Office: skipped: IP address was already set to 49.195.82.81.
```


## 使ddclient自动运行


```
service ddclient start
```

https://www.xibel-it.eu/how-to-use-ddclient-on-ubuntu/


## 清除　Cache

