

# Block

多层: 

*  /etc/hosts file
*  Chrome plugin
  +  force safe search 
  +  Leech block NG
*  OpenDNS


# Secure

*  强制插件: managed policy file
*  强制 /etc/hosts
*  Kill其他进程
*  自身防止被kill: 
  +  可以只有一个active进程，但是有多个守护进程负责: 
       +  update version
       +  检查checksum
       +  重新启动如果被kill
       +  检查block结果，通过DNS.


# Release

*  增加新website名单: 黑名单
*  Release new version: 从网络抓取
*  CI流程:
  +  修改 -> CI -> 新image -> 本地fetch, 部署
*  CI检查黑名单是否被恶意移除

# Monitoring & Alert

测试
*  不运行, 警告
*  测试DNS, 不符合结果, 警告