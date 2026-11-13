#
# pcat2d - Photonicat2 userspace display and device-control daemon
#
# Prebuilt-binary package: the aarch64 static binary is built by
# .github/workflows/build.yml and attached to the GitHub release v$(PKG_VERSION).
# Set PCAT2D_GH_USER below (or override on the make command line).
#

include $(TOPDIR)/rules.mk

PKG_NAME:=pcat2d
PKG_VERSION:=0.3.0
PKG_RELEASE:=1

PKG_LICENSE:=GPL-3.0-only
PKG_MAINTAINER:=Photonicat2 contributors
PKG_ARCH:=$(ARCH)

PCAT2D_GH_USER?=zhou1407
PKG_SOURCE:=$(PKG_NAME)-$(PKG_VERSION)-aarch64.tar.gz
PKG_SOURCE_URL:=https://github.com/$(PCAT2D_GH_USER)/pcat2-display-feed/releases/download/v$(PKG_VERSION)

# Filled by .github/workflows/build.yml after the release artifact is built.
# During local development the release tarball can be placed manually in
# $(TOPDIR)/dl/ and PKG_HASH can be set to "skip".
PKG_HASH:=skip

include $(INCLUDE_DIR)/package.mk

define Package/pcat2d
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=Photonicat2 display and device-control daemon
  DEPENDS:=+luci-base +rpcd
  CONFLICTS:=pcat2-display-mini-qm luci-app-pcat2 pcat2-display-mini
endef

define Package/pcat2d/description
  Userspace daemon for the Photonicat2 (RK3576) onboard GC9307 SPI display.
  Drives the display through /dev/spidev1.0, controls backlight, handles the
  RK805 pwrkey and the MCU (photonicat-pm) fan / charge-limit / power-LED /
  beeper controls, and exposes them to LuCI through a small ubus bridge.
endef

define Build/Compile
	# prebuilt binary is unpacked from $(PKG_BUILD_DIR)
endef

define Package/pcat2d/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/pcat2d $(1)/usr/bin/pcat2d
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/pcat2d.init $(1)/etc/init.d/pcat2d
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/pcat2d.config $(1)/etc/config/pcat2d
	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./files/rpcd-pcat2d $(1)/usr/libexec/rpcd/pcat2d
	$(INSTALL_DIR) $(1)/usr/share/luci/menu.d
	$(INSTALL_DATA) ./files/luci/menu.d/luci-app-pcat2d.json $(1)/usr/share/luci/menu.d/luci-app-pcat2d.json
	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_DATA) ./files/luci/acl.d/luci-app-pcat2d.json $(1)/usr/share/rpcd/acl.d/luci-app-pcat2d.json
	$(INSTALL_DIR) $(1)/www/luci-static/resources/view/pcat2d
	$(INSTALL_DATA) ./files/luci/view/pcat2d/control.js $(1)/www/luci-static/resources/view/pcat2d/control.js
endef

define Package/pcat2d/conffiles
/etc/config/pcat2d
endef

define Package/pcat2d/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	/etc/init.d/pcat2d enable
	/etc/init.d/pcat2d restart
	/etc/init.d/rpcd restart
	/etc/init.d/uhttpd restart >/dev/null 2>&1
}
endef

define Package/pcat2d/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	/etc/init.d/pcat2d stop
	/etc/init.d/pcat2d disable
}
endef

$(eval $(call BuildPackage,pcat2d))
