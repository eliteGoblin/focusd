

# MVP Q

* NextDNS how it works? does it need app? or I setup at my router level. 
* How the config auto-restore? I changed some config, then it restore back?
* How to release? python, pip? Easy to install, and configure like 

# Further 

* Disable the system, after motivation, for like 10 minutes, max 20 minutes? it will auto recover. 
* Consider Yubikey and Cloud Key as option to generate focusd key to add more friction
* Cloud storage to store released app, refine release process: local test => push to release => install via official releasse => auto protect system. 

# Tamper proof 

https://chatgpt.com/c/69775da2-58c8-8320-a949-0394a87f7195 

* LaunchDaemon, seems I need this instead of LaunchAgent?  make this optional, even with launchAgent, it still can working(lack permission will do servive degradation but still provide some level of protection, skill these protection which need system access)
