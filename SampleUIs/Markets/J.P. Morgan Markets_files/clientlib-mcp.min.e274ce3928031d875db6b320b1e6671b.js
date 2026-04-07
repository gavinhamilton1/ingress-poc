/*******************************************************************************
 * Copyright 2019 Adobe
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 ******************************************************************************/

/**
 * Element.matches()
 * https://developer.mozilla.org/enUS/docs/Web/API/Element/matches#Polyfill
 */
if (!Element.prototype.matches) {
    Element.prototype.matches = Element.prototype.msMatchesSelector || Element.prototype.webkitMatchesSelector;
}

// eslint-disable-next-line valid-jsdoc
/**
 * Element.closest()
 * https://developer.mozilla.org/enUS/docs/Web/API/Element/closest#Polyfill
 */
if (!Element.prototype.closest) {
    Element.prototype.closest = function(s) {
        "use strict";
        var el = this;
        if (!document.documentElement.contains(el)) {
            return null;
        }
        do {
            if (el.matches(s)) {
                return el;
            }
            el = el.parentElement || el.parentNode;
        } while (el !== null && el.nodeType === 1);
        return null;
    };
}

/*******************************************************************************
 * Copyright 2019 Adobe
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 ******************************************************************************/
(function() {
    "use strict";

    var containerUtils = window.CQ && window.CQ.CoreComponents && window.CQ.CoreComponents.container && window.CQ.CoreComponents.container.utils ? window.CQ.CoreComponents.container.utils : undefined;
    if (!containerUtils) {
        // eslint-disable-next-line no-console
        console.warn("Accordion: container utilities at window.CQ.CoreComponents.container.utils are not available. This can lead to missing features. Ensure the core.wcm.components.commons.site.container client library is included on the page.");
    }
    var dataLayerEnabled;
    var dataLayer;
    var delay = 100;

    var NS = "cmp";
    var IS = "accordion";

    var keyCodes = {
        ENTER: 13,
        SPACE: 32,
        END: 35,
        HOME: 36,
        ARROW_LEFT: 37,
        ARROW_UP: 38,
        ARROW_RIGHT: 39,
        ARROW_DOWN: 40
    };

    var selectors = {
        self: "[data-" + NS + '-is="' + IS + '"]'
    };

    var cssClasses = {
        button: {
            disabled: "cmp-accordion__button--disabled",
            expanded: "cmp-accordion__button--expanded"
        },
        panel: {
            hidden: "cmp-accordion__panel--hidden",
            expanded: "cmp-accordion__panel--expanded"
        }
    };

    var dataAttributes = {
        item: {
            expanded: "data-cmp-expanded"
        }
    };

    var properties = {
        /**
         * Determines whether a single accordion item is forced to be expanded at a time.
         * Expanding one item will collapse all others.
         *
         * @memberof Accordion
         * @type {Boolean}
         * @default false
         */
        "singleExpansion": {
            "default": false,
            "transform": function(value) {
                return !(value === null || typeof value === "undefined");
            }
        }
    };

    /**
     * Accordion Configuration.
     *
     * @typedef {Object} AccordionConfig Represents an Accordion configuration
     * @property {HTMLElement} element The HTMLElement representing the Accordion
     * @property {Object} options The Accordion options
     */

    /**
     * Accordion.
     *
     * @class Accordion
     * @classdesc An interactive Accordion component for toggling panels of related content
     * @param {AccordionConfig} config The Accordion configuration
     */
    function Accordion(config) {
        var that = this;

        if (config && config.element) {
            init(config);
        }

        /**
         * Initializes the Accordion.
         *
         * @private
         * @param {AccordionConfig} config The Accordion configuration
         */
        function init(config) {
            that._config = config;

            // prevents multiple initialization
            config.element.removeAttribute("data-" + NS + "-is");

            setupProperties(config.options);
            cacheElements(config.element);

            if (that._elements["item"]) {
                // ensures multiple element types are arrays.
                that._elements["item"] = Array.isArray(that._elements["item"]) ? that._elements["item"] : [that._elements["item"]];
                that._elements["button"] = Array.isArray(that._elements["button"]) ? that._elements["button"] : [that._elements["button"]];
                that._elements["panel"] = Array.isArray(that._elements["panel"]) ? that._elements["panel"] : [that._elements["panel"]];

                if (that._properties.singleExpansion) {
                    var expandedItems = getExpandedItems();
                    // multiple expanded items annotated, display the last item open.
                    if (expandedItems.length > 1) {
                        toggle(expandedItems.length - 1);
                    }
                }

                refreshItems();
                bindEvents();
                scrollToDeepLinkIdInAccordion();
            }
            if (window.Granite && window.Granite.author && window.Granite.author.MessageChannel) {
                /*
                 * Editor message handling:
                 * - subscribe to "cmp.panelcontainer" message requests sent by the editor frame
                 * - check that the message data panel container type is correct and that the id (path) matches this specific Accordion component
                 * - if so, route the "navigate" operation to enact a navigation of the Accordion based on index data
                 */
                window.CQ.CoreComponents.MESSAGE_CHANNEL = window.CQ.CoreComponents.MESSAGE_CHANNEL || new window.Granite.author.MessageChannel("cqauthor", window);
                window.CQ.CoreComponents.MESSAGE_CHANNEL.subscribeRequestMessage("cmp.panelcontainer", function(message) {
                    if (message.data && message.data.type === "cmp-accordion" && message.data.id === that._elements.self.dataset["cmpPanelcontainerId"]) {
                        if (message.data.operation === "navigate") {
                            // switch to single expansion mode when navigating in edit mode.
                            var singleExpansion = that._properties.singleExpansion;
                            that._properties.singleExpansion = true;
                            toggle(message.data.index);

                            // revert to the configured state.
                            that._properties.singleExpansion = singleExpansion;
                        }
                    }
                });
            }
        }

        /**
         * Displays the panel containing the element that corresponds to the deep link in the URI fragment
         * and scrolls the browser to this element.
         */
        function scrollToDeepLinkIdInAccordion() {
            if (containerUtils) {
                var deepLinkItemIdx = containerUtils.getDeepLinkItemIdx(that, "item", "item");
                if (deepLinkItemIdx > -1) {
                    var deepLinkItem = that._elements["item"][deepLinkItemIdx];
                    if (deepLinkItem && !deepLinkItem.hasAttribute(dataAttributes.item.expanded)) {
                        // if single expansion: close all accordion items
                        if (that._properties.singleExpansion) {
                            for (var j = 0; j < that._elements["item"].length; j++) {
                                if (that._elements["item"][j].hasAttribute(dataAttributes.item.expanded)) {
                                    setItemExpanded(that._elements["item"][j], false, true);
                                }
                            }
                        }
                        // expand the accordion item containing the deep link
                        setItemExpanded(deepLinkItem, true, true);
                    }
                    var hashId = window.location.hash.substring(1);
                    if (hashId) {
                        var hashItem = document.querySelector("[id='" + hashId + "']");
                        if (hashItem) {
                            hashItem.scrollIntoView();
                        }
                    }
                }
            }
        }

        /**
         * Caches the Accordion elements as defined via the {@code data-accordion-hook="ELEMENT_NAME"} markup API.
         *
         * @private
         * @param {HTMLElement} wrapper The Accordion wrapper element
         */
        function cacheElements(wrapper) {
            that._elements = {};
            that._elements.self = wrapper;
            var hooks = that._elements.self.querySelectorAll("[data-" + NS + "-hook-" + IS + "]");

            for (var i = 0; i < hooks.length; i++) {
                var hook = hooks[i];
                if (hook.closest("." + NS + "-" + IS) === that._elements.self) { // only process own accordion elements
                    var capitalized = IS;
                    capitalized = capitalized.charAt(0).toUpperCase() + capitalized.slice(1);
                    var key = hook.dataset[NS + "Hook" + capitalized];
                    if (that._elements[key]) {
                        if (!Array.isArray(that._elements[key])) {
                            var tmp = that._elements[key];
                            that._elements[key] = [tmp];
                        }
                        that._elements[key].push(hook);
                    } else {
                        that._elements[key] = hook;
                    }
                }
            }
        }

        /**
         * Sets up properties for the Accordion based on the passed options.
         *
         * @private
         * @param {Object} options The Accordion options
         */
        function setupProperties(options) {
            that._properties = {};

            for (var key in properties) {
                if (Object.prototype.hasOwnProperty.call(properties, key)) {
                    var property = properties[key];
                    var value = null;

                    if (options && options[key] != null) {
                        value = options[key];

                        // transform the provided option
                        if (property && typeof property.transform === "function") {
                            value = property.transform(value);
                        }
                    }

                    if (value === null) {
                        // value still null, take the property default
                        value = properties[key]["default"];
                    }

                    that._properties[key] = value;
                }
            }
        }

        /**
         * Binds Accordion event handling.
         *
         * @private
         */
        function bindEvents() {
            window.addEventListener("hashchange", scrollToDeepLinkIdInAccordion, false);
            var buttons = that._elements["button"];
            if (buttons) {
                for (var i = 0; i < buttons.length; i++) {
                    (function(index) {
                        buttons[i].addEventListener("click", function(event) {
                            toggle(index);
                            focusButton(index);
                        });
                        buttons[i].addEventListener("keydown", function(event) {
                            onButtonKeyDown(event, index);
                        });
                    })(i);
                }
            }
        }

        /**
         * Handles button keydown events.
         *
         * @private
         * @param {Object} event The keydown event
         * @param {Number} index The index of the button triggering the event
         */
        function onButtonKeyDown(event, index) {
            var lastIndex = that._elements["button"].length - 1;

            switch (event.keyCode) {
                case keyCodes.ARROW_LEFT:
                case keyCodes.ARROW_UP:
                    event.preventDefault();
                    if (index > 0) {
                        focusButton(index - 1);
                    }
                    break;
                case keyCodes.ARROW_RIGHT:
                case keyCodes.ARROW_DOWN:
                    event.preventDefault();
                    if (index < lastIndex) {
                        focusButton(index + 1);
                    }
                    break;
                case keyCodes.HOME:
                    event.preventDefault();
                    focusButton(0);
                    break;
                case keyCodes.END:
                    event.preventDefault();
                    focusButton(lastIndex);
                    break;
                case keyCodes.ENTER:
                case keyCodes.SPACE:
                    event.preventDefault();
                    toggle(index);
                    focusButton(index);
                    break;
                default:
                    return;
            }
        }

        /**
         * General handler for toggle of an item.
         *
         * @private
         * @param {Number} index The index of the item to toggle
         */
        function toggle(index) {
            var item = that._elements["item"][index];
            if (item) {
                if (that._properties.singleExpansion) {
                    // ensure only a single item is expanded if single expansion is enabled.
                    for (var i = 0; i < that._elements["item"].length; i++) {
                        if (that._elements["item"][i] !== item) {
                            var expanded = getItemExpanded(that._elements["item"][i]);
                            if (expanded) {
                                setItemExpanded(that._elements["item"][i], false);
                            }
                        }
                    }
                }
                setItemExpanded(item, !getItemExpanded(item));

                if (dataLayerEnabled) {
                    var accordionId = that._elements.self.id;
                    var expandedItems = getExpandedItems()
                        .map(function(item) {
                            return getDataLayerId(item);
                        });

                    var uploadPayload = { component: {} };
                    uploadPayload.component[accordionId] = { shownItems: expandedItems };

                    var removePayload = { component: {} };
                    removePayload.component[accordionId] = { shownItems: undefined };

                    dataLayer.push(removePayload);
                    dataLayer.push(uploadPayload);
                }
            }
        }

        /**
         * Sets an item's expanded state based on the provided flag and refreshes its internals.
         *
         * @private
         * @param {HTMLElement} item The item to mark as expanded, or not expanded
         * @param {Boolean} expanded true to mark the item expanded, false otherwise
         * @param {Boolean} keepHash true to keep the hash in the URL, false to update it
         */
        function setItemExpanded(item, expanded, keepHash) {
            if (expanded) {
                item.setAttribute(dataAttributes.item.expanded, "");
                var index = that._elements["item"].indexOf(item);
                if (!keepHash && containerUtils) {
                    containerUtils.updateUrlHash(that, "item", index);
                }
                if (dataLayerEnabled) {
                    dataLayer.push({
                        event: "cmp:show",
                        eventInfo: {
                            path: "component." + getDataLayerId(item)
                        }
                    });
                }

            } else {
                item.removeAttribute(dataAttributes.item.expanded);
                if (!keepHash && containerUtils) {
                    containerUtils.removeUrlHash();
                }
                if (dataLayerEnabled) {
                    dataLayer.push({
                        event: "cmp:hide",
                        eventInfo: {
                            path: "component." + getDataLayerId(item)
                        }
                    });
                }
            }
            refreshItem(item);
        }

        /**
         * Gets an item's expanded state.
         *
         * @private
         * @param {HTMLElement} item The item for checking its expanded state
         * @returns {Boolean} true if the item is expanded, false otherwise
         */
        function getItemExpanded(item) {
            return item && item.dataset && item.dataset["cmpExpanded"] !== undefined;
        }

        /**
         * Refreshes an item based on its expanded state.
         *
         * @private
         * @param {HTMLElement} item The item to refresh
         */
        function refreshItem(item) {
            var expanded = getItemExpanded(item);
            if (expanded) {
                expandItem(item);
            } else {
                collapseItem(item);
            }
        }

        /**
         * Refreshes all items based on their expanded state.
         *
         * @private
         */
        function refreshItems() {
            for (var i = 0; i < that._elements["item"].length; i++) {
                refreshItem(that._elements["item"][i]);
            }
        }

        /**
         * Returns all expanded items.
         *
         * @private
         * @returns {HTMLElement[]} The expanded items
         */
        function getExpandedItems() {
            var expandedItems = [];

            for (var i = 0; i < that._elements["item"].length; i++) {
                var item = that._elements["item"][i];
                var expanded = getItemExpanded(item);
                if (expanded) {
                    expandedItems.push(item);
                }
            }

            return expandedItems;
        }

        /**
         * Annotates the item and its internals with
         * the necessary style and accessibility attributes to indicate it is expanded.
         *
         * @private
         * @param {HTMLElement} item The item to annotate as expanded
         */
        function expandItem(item) {
            var index = that._elements["item"].indexOf(item);
            if (index > -1) {
                var button = that._elements["button"][index];
                var panel = that._elements["panel"][index];
                button.classList.add(cssClasses.button.expanded);
                // used to fix some known screen readers issues in reading the correct state of the 'aria-expanded' attribute
                // e.g. https://bugs.webkit.org/show_bug.cgi?id=210934
                setTimeout(function() {
                    button.setAttribute("aria-expanded", true);
                }, delay);
                panel.classList.add(cssClasses.panel.expanded);
                panel.classList.remove(cssClasses.panel.hidden);
                panel.setAttribute("aria-hidden", false);
            }
        }

        /**
         * Annotates the item and its internals with
         * the necessary style and accessibility attributes to indicate it is not expanded.
         *
         * @private
         * @param {HTMLElement} item The item to annotate as not expanded
         */
        function collapseItem(item) {
            var index = that._elements["item"].indexOf(item);
            if (index > -1) {
                var button = that._elements["button"][index];
                var panel = that._elements["panel"][index];
                button.classList.remove(cssClasses.button.expanded);
                // used to fix some known screen readers issues in reading the correct state of the 'aria-expanded' attribute
                // e.g. https://bugs.webkit.org/show_bug.cgi?id=210934
                setTimeout(function() {
                    button.setAttribute("aria-expanded", false);
                }, delay);
                panel.classList.add(cssClasses.panel.hidden);
                panel.classList.remove(cssClasses.panel.expanded);
                panel.setAttribute("aria-hidden", true);
            }
        }

        /**
         * Focuses the button at the provided index.
         *
         * @private
         * @param {Number} index The index of the button to focus
         */
        function focusButton(index) {
            var button = that._elements["button"][index];
            button.focus();
        }
    }

    /**
     * Reads options data from the Accordion wrapper element, defined via {@code data-cmp-*} data attributes.
     *
     * @private
     * @param {HTMLElement} element The Accordion element to read options data from
     * @returns {Object} The options read from the component data attributes
     */
    function readData(element) {
        var data = element.dataset;
        var options = [];
        var capitalized = IS;
        capitalized = capitalized.charAt(0).toUpperCase() + capitalized.slice(1);
        var reserved = ["is", "hook" + capitalized];

        for (var key in data) {
            if (Object.prototype.hasOwnProperty.call(data, key)) {
                var value = data[key];

                if (key.indexOf(NS) === 0) {
                    key = key.slice(NS.length);
                    key = key.charAt(0).toLowerCase() + key.substring(1);

                    if (reserved.indexOf(key) === -1) {
                        options[key] = value;
                    }
                }
            }
        }

        return options;
    }

    /**
     * Parses the dataLayer string and returns the ID
     *
     * @private
     * @param {HTMLElement} item the accordion item
     * @returns {String} dataLayerId or undefined
     */
    function getDataLayerId(item) {
        if (item) {
            if (item.dataset.cmpDataLayer) {
                return Object.keys(JSON.parse(item.dataset.cmpDataLayer))[0];
            } else {
                return item.id;
            }
        }
        return null;
    }

    /**
     * Document ready handler and DOM mutation observers. Initializes Accordion components as necessary.
     *
     * @private
     */
    function onDocumentReady() {
        dataLayerEnabled = document.body.hasAttribute("data-cmp-data-layer-enabled");
        dataLayer = (dataLayerEnabled) ? window.adobeDataLayer = window.adobeDataLayer || [] : undefined;

        var elements = document.querySelectorAll(selectors.self);
        for (var i = 0; i < elements.length; i++) {
            new Accordion({ element: elements[i], options: readData(elements[i]) });
        }

        var MutationObserver = window.MutationObserver || window.WebKitMutationObserver || window.MozMutationObserver;
        var body = document.querySelector("body");
        var observer = new MutationObserver(function(mutations) {
            mutations.forEach(function(mutation) {
                // needed for IE
                var nodesArray = [].slice.call(mutation.addedNodes);
                if (nodesArray.length > 0) {
                    nodesArray.forEach(function(addedNode) {
                        if (addedNode.querySelectorAll) {
                            var elementsArray = [].slice.call(addedNode.querySelectorAll(selectors.self));
                            elementsArray.forEach(function(element) {
                                new Accordion({ element: element, options: readData(element) });
                            });
                        }
                    });
                }
            });
        });

        observer.observe(body, {
            subtree: true,
            childList: true,
            characterData: true
        });
    }

    if (document.readyState !== "loading") {
        onDocumentReady();
    } else {
        document.addEventListener("DOMContentLoaded", onDocumentReady);
    }

    if (containerUtils) {
        window.addEventListener("load", containerUtils.scrollToAnchor, false);
    }

}());

document.addEventListener("DOMContentLoaded", function() {
    let ctaButton = document.getElementById("cta-button-burger-menu-desktop");
    let dropdownMenu = document.getElementById("dropdown-menu");
    let desktopMenu = document.querySelector('.desktop-navigation');
    const enableSrollingString = desktopMenu?.getAttribute('data-enable-scrolling');
    const enableSrolling = enableSrollingString === 'true' ? true : false;
    let menuOriginalLabel = null;
    if (ctaButton) {
        ctaButton.addEventListener("click", function(event) {
            event.preventDefault();
            if (!menuOriginalLabel) {
                menuOriginalLabel = ctaButton.getAttribute('title') || ctaButton.getAttribute('aria-label');
            }
            this.classList.toggle('active');
            dropdownMenu.classList.toggle('show');
            if (enableSrolling !== true) {
                document.body.classList.toggle('menu-is-active');
            } 
    
            if (isDesktop() === false) {
                 document.body.classList.toggle('menu-is-active');
            }
            if (this.classList.contains('active')) {
                ctaButton.setAttribute('title', 'Click or press Enter to close main menu.');
                ctaButton.setAttribute('aria-label', 'Click or press Enter to close main menu.');
            } else {
                ctaButton.setAttribute('title', menuOriginalLabel);
                ctaButton.setAttribute('aria-label', menuOriginalLabel);
            }
        });
        ctaButton.addEventListener('keydown', function(event) {
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                ctaButton.click();
            }
        });
        
    
    }
    // Add click event listener to all parent links
    let currentOpen = null;
    function closeCurrentOpen() {
        if (currentOpen) {
            currentOpen.classList.remove('show');
            currentOpen = null;
        }
    }

    // Tabbing
    function menuFocus(menuDesktop) {
       
        menuDesktop.addEventListener('keydown', (e) => {
            menuButton = document.getElementById("cta-button-burger-menu-desktop").classList.contains('active');
            closeMenuButton = document.querySelector('.close-desktop-nav');
            const allLis = Array.from(document.querySelectorAll('li.mcp-navigation_level-1'));
            const visibleLis = allLis.filter(li => li.offsetParent !== null);
            const lastVisibleLi = visibleLis[visibleLis.length - 1];
            const lastEl = lastVisibleLi?.querySelector('a[href]');
            const lastElInMenu = lastEl?.id === 'd_lvl-1-digital-reporting'
            if (e.key === 'Tab') {
                var firstElMainMenu = menuDesktop.querySelector('.mcp-navigation_level-0:first-child button');
                var lastElMainMenu = menuDesktop.querySelector('.mcp-navigation_level-0:last-child button');
                var firstEl = menuDesktop.querySelector('.mcp-navigation_level-0.active button');
                var firstElHolder = menuDesktop.querySelector('.mcp-navigation_level-0.active');
                var nextfirstElHolder = firstElHolder?.nextElementSibling ? firstElHolder?.nextElementSibling : menuDesktop.querySelector('.mcp-navigation_level-0:not(.has-children)');
                var nextfirstElLink = nextfirstElHolder.querySelector('button');
                if (e.shiftKey) {
                    if (document.activeElement === firstElMainMenu) {
                        e.preventDefault();
                        closeMenuButton.focus();
                    }
                } else {
                    if (lastEl) {
                        if (document.activeElement === lastEl) {
                            if (nextfirstElLink) {
                                e.preventDefault();
                                nextfirstElLink.focus();
                            }  
                        } 
                    } 
                    else if (lastElInMenu) {
                        if (document.activeElement === lastElInMenu) {
                            if (lastElMainMenu) {
                                e.preventDefault();
                                lastElMainMenu.focus();
                               
                            }  
                        }
                    }
                    if (document.activeElement === closeMenuButton) {
                        e.preventDefault();
                        firstElMainMenu.focus();    
                    }
                    if (document.activeElement === lastElMainMenu && !lastElInMenu ) {
                        e.preventDefault();
                        closeMenuButton.focus();    
                    }
                    if (document.activeElement === menuButton) {
                        e.preventDefault();
                        firstEl.focus();    
                    }
                }
            }
    
            if (e.key === 'Escape') {
                document.querySelector('.close-desktop-nav').click();
                document.body.classList.remove('menu-is-active');
                dropdownMenu.classList.remove('show');
                ctaButton.classList.remove('active');
                ctaButton.setAttribute('title', menuOriginalLabel);
                ctaButton.setAttribute('aria-label', menuOriginalLabel);
                menuOriginalLabel = null;
            }
        });
    
    }
    

    document.querySelectorAll('.left-side-navigation .mcp-navigation_level-0-link').forEach(function(link) {
        link.addEventListener('click', function(event) {
            event.preventDefault();
            let targetId = this.getAttribute('data-toggle');
            let targetElement = document.getElementById(targetId);
            closeCurrentOpen();
            if (targetElement) {
                if (targetElement.classList.contains('show')) {
                    targetElement.classList.remove('show');
                } else {
                    targetElement.classList.add('show');
                    currentOpen = targetElement;
                }
                const firstSubLink = targetElement.querySelector('a, [role="button"]');
                firstSubLink && firstSubLink.focus();
            }
            document.querySelectorAll('.mcp-navigation_level-0').forEach(function(mainMenuLi) {
                mainMenuLi.classList.remove('active');
            });

            let outerDiv = this.closest('.mcp-navigation_level-0');
            if (outerDiv) {
                if (outerDiv.classList.contains('active')) {
                    outerDiv.classList.remove('active');
                } else {
                    outerDiv.classList.add('active');
                    currentOpen = targetElement;
                }
            }

        });
        link.addEventListener('keydown', function(event) {
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                link.click();
            }
        });
    });
    // Add 'open' class to the first submenu by default
    let firstSubmenu = document.querySelector('.mcp-navigation_group_level-1.submenu');
    let firstSubmenuButton = document.querySelector('.mcp-navigation_level-0');
    if (firstSubmenu) {
        firstSubmenuButton.classList.add('active');
        firstSubmenu.classList.add('show');
        currentOpen = firstSubmenu;
    }
    if (firstSubmenuButton) {
        firstSubmenuButton.classList.add('active');
    }

    document.querySelectorAll('.close-desktop-nav').forEach(function(close) {
        close.addEventListener('click', function(event) {
           if (dropdownMenu) {
                if (dropdownMenu.classList.contains('show')) {
                    ctaButton.setAttribute('title', menuOriginalLabel);
                    ctaButton.setAttribute('aria-label', menuOriginalLabel);
                    dropdownMenu.classList.remove('show');
                    ctaButton.classList.remove('active');
                    document.body.classList.remove('menu-is-active'); 
                }
            }
        });
        close.addEventListener('keydown', function(event) {
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                close.click();
            }
        });
    });
    document.querySelectorAll('.accordion-mcp-navigation_level-0-link').forEach(function(link) {
        link.addEventListener('click', function(event) {
            let targetId = this.getAttribute('data-toggle');
            let targetElement = document.getElementById('mobile-'+targetId);
            closeCurrentOpen();
            if (targetElement) {
                if (targetElement.classList.contains('show')) {
                    targetElement.classList.remove('show');
                } else {
                    targetElement.classList.add('show');
                    currentOpen = targetElement;
                }
            }


            let outerDiv = this.closest('.accordion-mcp-navigation_level-0');
            if (outerDiv) {
                if (outerDiv.classList.contains('active')) {
                    outerDiv.classList.remove('active');
                    targetElement.classList.remove('show');
                } else {
                    outerDiv.classList.add('active');
                    currentOpen = targetElement;
                }
            }

        });
        link.addEventListener('keydown', function(event) {
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                link.click();
            }
        });
    });
    document.querySelectorAll(".accordion-mcp-navigation_level-0-link span").forEach(el => el.addEventListener('click', event => {
        if(event.target.getAttribute("data-href")){
            let realUrl = event.target.getAttribute("data-href");
            let relativeUrl = realUrl.substring(realUrl.lastIndexOf("/") + 1);
            if (relativeUrl.endsWith(".html")) {
                relativeUrl = relativeUrl.replace(".html", "");
            }
            window.location.href = window.location.origin+'/'+relativeUrl;
        }
    }));
    document.querySelectorAll(".accordion-mcp-navigation_level-0-link span").forEach(el => el.addEventListener('keydown', event => {
        if( (event.key === 'Enter' || event.key === 13 || event.key === 32) && event.target.getAttribute("data-href")){
            window.location.href = window.location.origin + event.target.getAttribute("data-href")
        }
    }));
    menuFocus(desktopMenu);
    function closeMenuOnScroll() {
        window.addEventListener('scroll', function(event) {
            if( isDesktop() === true && enableSrolling === true && window.scrollY > 20 ) {
                document.querySelector('.close-desktop-nav').click();
                document.body.classList.remove('menu-is-active');
                dropdownMenu.classList.remove('show');
                ctaButton.classList.remove('active');
                ctaButton.setAttribute('title', menuOriginalLabel);
                ctaButton.setAttribute('aria-label', menuOriginalLabel);
                menuOriginalLabel = null;
            }
        });
        
    }
    closeMenuOnScroll();
    window.addEventListener('resize', closeMenuOnScroll);
});
document.addEventListener('DOMContentLoaded', function() {
    if (document.querySelector('.hero-homepage')) {
        const heroComponent = document.querySelector('.hero-homepage');
        const buttons = heroComponent.querySelectorAll('.pillar-title');
        if (buttons) {
            buttons.forEach(button => {
                let heroOriginalAdaLabel = null;
                let heroOriginalAdaLabelClose = null
                button.addEventListener('click', function() {
                    const isActive = button.classList.contains('active');
                    if (!heroOriginalAdaLabel) {
                        heroOriginalAdaLabel = button.querySelector('.vertical-title-wrapper').getAttribute('title') || button.querySelector('.vertical-title-wrapper').getAttribute('aria-label');
                    }
                    heroOriginalAdaLabelClose = heroOriginalAdaLabel.replace('expand', 'close');

                    buttons.forEach(btn => {
                        btn.classList.remove('active');
                        let container = btn.nextElementSibling;
                        while (container && !container.classList.contains('pillar-content-wrapper')) {
                            container = container.nextElementSibling;
                        }
                        if (container) {
                            container.classList.remove('active');
                        }
                    });

                    if (!isActive) {
                        let container = button.nextElementSibling;
                        while (container && !container.classList.contains('pillar-content-wrapper')) {
                            container = container.nextElementSibling;
                        }
                        if (container) {
                            button.classList.add('active');
                            container.classList.add('active');
                            button.querySelector('.vertical-title-wrapper').setAttribute('title', heroOriginalAdaLabelClose);
                            button.querySelector('.vertical-title-wrapper').setAttribute('aria-label', heroOriginalAdaLabelClose);
                            heroOriginalAdaLabelClose = null;
                        }
                    } else {
                        button.querySelector('.vertical-title-wrapper').setAttribute('title', heroOriginalAdaLabel);
                        button.querySelector('.vertical-title-wrapper').setAttribute('aria-label', heroOriginalAdaLabel);
                        heroOriginalAdaLabel = null;
                    }
                });
                button.addEventListener('keydown', function(event) {
                    if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                        button.click();
                    }
                });
            });
        }
    }
});

function waitForElement(selector) {
    return new Promise(resolve => {
        if (document.querySelector(selector)) {
            return resolve(document.querySelector(selector));
        }
        const observer = new MutationObserver(mutations => {
            if (document.querySelector(selector)) {
                resolve(document.querySelector(selector));
                observer.disconnect();
            }
        });
        observer.observe(document.body, {
            childList: true,
            subtree: true
        });
    });
}

function convertBrightcoveDuration(durationMs) {
    const totalSeconds = Math.floor(durationMs);
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const seconds = totalSeconds % 60;
    const formattedTime = 
        String(hours).padStart(2, '0') + ":" + 
        String(minutes).padStart(2, '0') + ":" + 
        String(seconds).padStart(2, '0');

    return formattedTime;
}

function isDesktop (){
    return window.matchMedia('(min-width: 992px)').matches;
}

// Prevent browser's default anchor jump on page load
if ('scrollRestoration' in history) {
    history.scrollRestoration = 'manual';
}
if (window.location.hash) {
    setTimeout(() => {
        window.scrollTo(0, 0);
    }, 1);
}

document.addEventListener("DOMContentLoaded", function () {
    const selector = `[data-dpa-asset-meta-component_id="${window.location.hash.slice(1)}"]`;
    if (window.location.hash) {
        waitForElement(selector).then(element => {
            setTimeout(() => {
                element.scrollIntoView({ behavior: "smooth", block: "start", inline: "nearest" });
            }, 400);
        });
    }
});
if (document.querySelector('.back-button')) {
    var backButtons = document.querySelectorAll('.back-button');
    backButtons.forEach(function(backBtn) {
        backBtn.addEventListener('click', function(){
            window.history.back();
        });
        backBtn.addEventListener('keydown', function(event) {
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                ctaButton.click();
            }
        });
    });
}

document.addEventListener('DOMContentLoaded', function() {
    if (document.querySelector('.hero')) {
        const heroHtml = document.querySelector('.hero');
        const buttons = heroHtml.querySelectorAll('.pillar-title');
        if (buttons) {
            buttons.forEach(button => {
                button.addEventListener('click', function() {
                    const isActive = button.classList.contains('active');

                    buttons.forEach(btn => {
                        btn.classList.remove('active');
                        let container = btn.previousElementSibling;
                        while (container && !container.classList.contains('pillar-content-wrapper')) {
                            container = container.previousElementSibling;
                        }
                        if (container) {
                            container.classList.remove('active');
                        }
                    });

                    if (!isActive) {
                        let container = button.previousElementSibling;
                        while (container && !container.classList.contains('pillar-content-wrapper')) {
                            container = container.previousElementSibling;
                        }
                        if (container) {
                            button.classList.add('active');
                            container.classList.add('active');
                        }
                    }
                });
                button.addEventListener('keydown', function(event) {
                    if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                        button.click();
                    }
                });
            });
        }
    }
});


function createBrightcovePlayers() {
    const brightcoveVideoPlayers = document.querySelectorAll('.brightcove-mcp-video-player');
    brightcoveVideoPlayers.forEach((brightcovePlayer) => {
        const videoID = brightcovePlayer.getAttribute("data-video-id");
        const uniqueID = 'brightcove-mcp-video-player-'+videoID+'';
        const bPlayer = videojs(uniqueID);
        const muteButton = brightcovePlayer.querySelector('.vjs-mute-control');
        const playButton = brightcovePlayer.querySelector('.vjs-play-control');
        let mediaControls = brightcovePlayer.closest('.video-poster');
        if(mediaControls) {
            mediaControls.querySelectorAll('.video-dsc-btn').forEach(button => {
                if(button.classList.contains('cmp-brightcove__video--desc--btn')) {
                    button.addEventListener('click', function(e) {
                        e.stopPropagation();
                        this.classList.add('hide');
                        mediaControls.querySelector('.cmp-brightcove__hide--video--desc--btn').classList.remove('hide');
                        bPlayer.muted(false);
                        
                    });
                }
                if(button.classList.contains('cmp-brightcove__hide--video--desc--btn')) {
                    button.addEventListener('click', function(e) {
                        e.stopPropagation();
                        this.classList.add('hide');
                        mediaControls.querySelector('.cmp-brightcove__video--desc--btn').classList.remove('hide');
                        bPlayer.muted(true);
                    });
                }
                
            });
            
            muteButton.addEventListener('click', function(e) {
                if(this.classList.contains('vjs-vol-0')) {
                    mediaControls.querySelector('.cmp-brightcove__video--desc--btn').click();
                } else {
                    mediaControls.querySelector('.cmp-brightcove__hide--video--desc--btn').click();
                }
            });
            playButton.addEventListener('click', function(e) {
                pauseAllVideos (videoID); 
            });
        }
        bPlayer.on("loadedmetadata", function() {          
            let podcastContainer = brightcovePlayer.closest('.mcp-podcast-container');       
            let timeDuration = (podcastContainer && podcastContainer.querySelector('.media-duration'));
            if(timeDuration) {
                timeDuration.textContent =  convertBrightcoveDuration(bPlayer.mediainfo.duration);
            }
        })
    });
}

function pauseAllVideos (playingVideoId) {
    document.querySelectorAll('video').forEach(function(inneritem) {
        if (inneritem.getAttribute("data-video-id") != playingVideoId) {
            inneritem.pause();
            const playButons = document.querySelectorAll('.play-button');
            playButons.forEach((playButon) => {
                playButon.classList.remove('playing');
            });
        }
    });
}

if (document.querySelector('.video-poster')) {
    let currentlyPlayingVideo = null;  
    let playingVideoIdPoster = null; 
    const videosWithPoster = document.querySelectorAll('.video-poster');
    videosWithPoster.forEach((videoPoster) => {
        videoPoster.classList.add('hide');
        waitForElement('.video-js').then((elm) => {
            const videoPlayer = videoPoster.querySelector('video');
            playingVideoIdPoster = videoPlayer.getAttribute("data-video-id");
            if (!videoPlayer) return;
            videoPoster.classList.remove('hide');
            if (videoPlayer.paused) {
                //videoPlayer.play();
                currentlyPlayingVideo = videoPlayer;
            } else {
                videoPlayer.pause();
                currentlyPlayingVideo = null;
            }
            videoPoster.querySelectorAll('.clickArea, .overlay-play-button').forEach(button => { 
                button.addEventListener('click', function(e) {
                    e.stopPropagation();   
                    this.closest('.poster-container').classList.add('hide');
                    videoPoster.querySelector('.player-embed-wrap').classList.remove('hide');
                    pauseAllVideos (playingVideoIdPoster);
                    videoPoster.querySelector('.cmp-brightcove__hide--video--desc--btn').classList.remove('hide');
                    videoPoster.querySelector('.cmp-brightcove__video--desc--btn').classList.add('hide');
                    videoPlayer.play();
                });
            });
            videoPoster.querySelectorAll('.transcript-btn').forEach(button => {
                if(button.classList.contains('cmp-brightcove__transcript--btn')) {
                    button.addEventListener('click', function(e) {
                        e.stopPropagation();
                        this.classList.add('hide');
                        videoPoster.querySelector('.cmp-brightcove__hide--transcript--btn').classList.remove('hide');
                        videoPoster.querySelector('.cmp-brightcove__transcript').classList.remove('hide');
                        videoPoster.querySelector('.cmp-brightcove__transcript').focus();
                    });
                }
                if(button.classList.contains('cmp-brightcove__hide--transcript--btn')) {
                    button.addEventListener('click', function(e) {
                        e.stopPropagation();
                        this.classList.add('hide');
                        videoPoster.querySelector('.cmp-brightcove__transcript--btn').classList.remove('hide');
                        videoPoster.querySelector('.cmp-brightcove__transcript').classList.add('hide');
                        videoPoster.querySelector('.cmp-brightcove__transcript--btn .cmp-brightcove__transcript--btn--text').focus();
                    });
                }
            })
        });
        if (document.querySelector('.media-controls-ada')) {
            function modalFocusTranscipt(modal) {
                const hideTranscript = modal.querySelector('.cmp-brightcove__hide--transcript--btn .cmp-brightcove__transcript--btn--text');
                const transcriptContent = modal.querySelector('.cmp-brightcove__transcript');
                function handleTabT(event) {
                if (event.key === 'Tab') {                          
                    if (event.shiftKey) {
                        if (document.activeElement === transcriptContent) {
                            event.preventDefault();
                            hideTranscript.focus({focusVisible: true});
                        }
                    } else {
                        if (document.activeElement === transcriptContent) {
                            event.preventDefault();
                            hideTranscript.focus({focusVisible: true});
                        }
                    }
                } 
                
                }
                modal.addEventListener('keydown', handleTabT);
            }
            
        }
        modalFocusTranscipt(videoPoster)
    });
};





// PODCAST

document.addEventListener('DOMContentLoaded', function() {
    const podcasts = document.querySelectorAll(".wave__wrapper");
    let currentlyPlayingVideo = null;
    podcasts.forEach((podcast) => {
        waitForElement(".video-js").then(() => {
            podcast.classList.add('show');
            document.querySelectorAll('.play-me').forEach(button => {
                let originalLabel = null;  
                button.addEventListener('click', function() {
                  if (!originalLabel) {
                    originalLabel = button.getAttribute('title') || button.getAttribute('aria-label');
                  }
                  const video = this.closest('.wave__wrapper').querySelector('video');
                  playingVideoIdPodcast = video.getAttribute("data-video-id");
                  if (!video) return;
            
                  if (currentlyPlayingVideo && currentlyPlayingVideo !== video) {
                    currentlyPlayingVideo.pause();
                    currentlyPlayingVideo.closest('.wave__wrapper').querySelector('.play-button').classList.remove('playing');
                  }
                  if (video.paused) {
                    pauseAllVideos (playingVideoIdPodcast)
                    video.play();
                    currentlyPlayingVideo = video;
                    this.querySelector('.play-button').classList.add('playing');
                    button.setAttribute('title', 'Stop playing');
                    button.setAttribute('aria-label', 'Stop playing');
                  } else {
                    video.pause();
                    currentlyPlayingVideo = null;
                    this.querySelector('.play-button').classList.remove('playing');
                    button.setAttribute('title', originalLabel);
                    button.setAttribute('aria-label', originalLabel);
                    originalLabel = null;
                  }
                });
            });
              
        });

    });

});

document.body.addEventListener('click', function (event) {
  if (event.target.classList.contains('vjs-play-control') || event.target.classList.contains('vjs-paused')) {
    const bcPlayButton = event.target;
    const bcPlayButtonId = bcPlayButton.closest('.video-js').getAttribute('data-video-id');
    console.log('click play' + bcPlayButtonId)
    pauseAllVideos (bcPlayButtonId);
  }
});

function appendBackdropModal() {
    const div = document.createElement('div');
    div.className = 'modal-backdrop fade';
    document.body.appendChild(div);
}
function removeBackdropModal() {
    document.body.querySelector('.modal-backdrop').remove();
}

document.addEventListener('DOMContentLoaded', function() {
    createBrightcovePlayers();
    
});
const viewportWidth = window.innerWidth;
function makeBreadcrumbSticky() {
    window.addEventListener('DOMContentLoaded', (event) => {
        if (document.querySelector('.mcp-hero-teaser__breadcrumb')) {
            const myDiv = document.querySelector('.mcp-hero-teaser__breadcrumb');
            const parentDiv = myDiv.parentElement;
            const originalTop = myDiv.getBoundingClientRect().top + window.scrollY;
            if (!myDiv) {
                return;
            }
            window.addEventListener('scroll', function() {
                const scrollPosition = window.scrollY || document.documentElement.scrollTop;
                if (scrollPosition >= originalTop - 100) {
                    myDiv.style.position = 'fixed';
                    myDiv.style.top = '84px';
                    myDiv.classList.add('fixed')
                }
                else {
                    myDiv.classList.remove('fixed')
                    myDiv.style.position = 'absolute';
                    myDiv.style.top = '';
                }
            });
        }
    });
}
if (viewportWidth > 991) {
    makeBreadcrumbSticky();
}

document.addEventListener("DOMContentLoaded", function () {
    const heroTeasers = document.querySelectorAll(".mcp-hero-teaser");
    heroTeasers.forEach((heroTeaser) => {
        heroTeaser.querySelectorAll('.js-pause-button').forEach(button => {
            button.addEventListener('click', function() {
              this.style.display = 'none';
              heroTeaser.querySelector('.js-play-button').style.display = 'flex';
              heroTeaser.querySelector('.hero-image-animation').style.display = 'none';
              heroTeaser.querySelector('.hero-image-cover').style.display = 'block';
            });
          });
        heroTeaser.querySelectorAll('.js-play-button').forEach(button => {
            button.addEventListener('click', function() {
              this.style.display = 'none';
              heroTeaser.querySelector('.js-pause-button').style.display = 'flex';
              heroTeaser.querySelector('.hero-image-animation').style.display = 'block';
              heroTeaser.querySelector('.hero-image-cover').style.display = 'none';
            });
        });

    });
});
document.addEventListener("DOMContentLoaded", function () {
  const sections = document.querySelectorAll(".capabilities-section");
  sections.forEach((section) => {
    const cards = section.querySelectorAll(".cards__item");
    const backgroundImages = section.querySelectorAll(".background-layer__image");
    let currentIndex = 0;
    let intervalId;
    let slideTime;
    let autoSlide;

    function changeCard() {
      cards.forEach((card) => card.classList.remove("active"));
      cards[currentIndex].classList.add("active");
      const imageId = cards[currentIndex].getAttribute("data-id");
      backgroundImages.forEach((img) => img.classList.remove("active"));
      const matchingImage = section.querySelector(`.background-layer__image[data-image-id="${imageId}"]`);
      if (matchingImage) {
        matchingImage.classList.add("active");
      }
      currentIndex++;
      if (currentIndex >= cards.length) {
        currentIndex = 0;
      }
    }

    let cardsWrapper = section.querySelectorAll(".cards");

    function startAutoSlide(slideTime) {
      intervalId = setInterval(changeCard, slideTime);
    }

    function stopAutoSlide() {
      clearInterval(intervalId);
    }

    cardsWrapper.forEach((cardWrapper) => {
      autoSlide = cardWrapper.getAttribute("data-auto-slide");
      if (autoSlide) {
        slideTime = cardWrapper.getAttribute("data-slide-time") || 3000;
        startAutoSlide(slideTime);
      }
    });

    changeCard();
    cards.forEach((card, index) => {
      card.addEventListener("click", function () {
        cards.forEach((c) => c.classList.remove("active"));
        card.classList.add("active");
        const imageId = card.getAttribute("data-id");
        backgroundImages.forEach((img) => img.classList.remove("active"));
        const matchingImage = section.querySelector(`.background-layer__image[data-image-id="${imageId}"]`);
        if (matchingImage) {
          matchingImage.classList.add("active");
        }
        currentIndex = index;
      });
      card.addEventListener('keydown', function(event) {
        if (event.key === 'Enter' || event.key === '13' || event.key === '32') {
          card.click();
        }
      });
      if (autoSlide) {
        card.addEventListener("mouseenter", function () {
          stopAutoSlide();
        });
        card.addEventListener("mouseleave", function () {
          startAutoSlide(slideTime);
          section.querySelector('.js-pause-button').style.display = 'flex';
          section.querySelector('.js-play-button').style.display = 'none';
        });
      }
    });
    document.querySelectorAll('#capabilitiesBtn').forEach(button => {
      button.addEventListener('click', function() {
        if (this.classList.contains('pause')) {
          this.classList.remove('pause');
          startAutoSlide(slideTime);
        } else {
          this.classList.add('pause');
          stopAutoSlide();
        }
        
      });
    });
    document.querySelectorAll('.js-pause-button').forEach(button => {
      button.addEventListener('click', function() {
        stopAutoSlide();
        this.style.display = 'none';
        section.querySelector('.js-play-button').style.display = 'flex';
        section.querySelector('.js-play-button').focus({focusVisible: true});
      });
    });
    document.querySelectorAll('.js-play-button').forEach(button => {
      button.addEventListener('click', function() {
        startAutoSlide(slideTime);
        this.style.display = 'none';
        section.querySelector('.js-pause-button').style.display = 'flex';
        section.querySelector('.js-pause-button').focus({focusVisible: true});
      });
    });
    
  });
});


if (document.querySelector('.mcp-insights__carousel')) {
        const carouselItems = document.querySelectorAll('.swiper-slide');
        if (carouselItems.length > 3) {
            const swiper = new Swiper('.mcp-insights__carousel', {
                slidesPerView: 'auto',
                spaceBetween: 24,
                slidesPerGroup: 1,
                loop: false,               
                pagination: {
                  el: '.mcp-insights__carousel .swiper-pagination',
                  clickable: true,       
                },
                a11y: {
                  scrollOnFocus: false,
                },
                navigation: {
                  nextEl: '.mcp-insights__carousel .swiper-button-next',
                  prevEl: '.mcp-insights__carousel .swiper-button-prev',
                },
                on: {
                  init: function() {
                    updateAriaLabels (this);
                    setupLiveRegion(this);
                  },
                  slideChange: function() {
                    updateAriaLabels (this);
                    updateLiveRegion(this);
                  }
                }
                
            });

            function toggleNavButtonsBasedOnBullets(swiper) {
                const nextButton = document.querySelector('.mcp-insights__carousel .swiper-button-next');
                const prevButton = document.querySelector('.mcp-insights__carousel .swiper-button-prev');
              
                const totalBullets = swiper.pagination.bullets.length;
                const activeBulletIndex = Array.from(swiper.pagination.bullets).findIndex(bullet => bullet.classList.contains('swiper-pagination-bullet-active'));
              
                if (activeBulletIndex === 0) {
                  prevButton.classList.add('swiper-button-disabled');
                } else {
                  prevButton.classList.remove('swiper-button-disabled');
                }
              
                if (activeBulletIndex === totalBullets - 1) {
                  nextButton.classList.add('swiper-button-disabled');
                } else {
                  nextButton.classList.remove('swiper-button-disabled');
                }
            }

            function updateAriaLabels(swiperInstance) {
              const slides = swiperInstance.slides;
              const bullets = swiperInstance.pagination.bullets;
        
              bullets.forEach((bullet, index)=> {
                const slide = slides[index];
                const h3 = slide.querySelector('.card-title h3');
                const title = h3 ? h3.textContent.trim() : '';
        
                const baseLabel = `Go to slide ${index + 1}`;
                const newLabel = title ? `${baseLabel}: ${title}` : baseLabel;
        
                bullet.setAttribute('aria-label', newLabel);
                });
            }
            function setupLiveRegion(swiperInstance) {
              const container = swiperInstance.el;
              if (!container.querySelector('.swiper-live-region')) {
                const liveRegion = document.createElement('div');
                liveRegion.className = 'swiper-live-region visually-hidden';
                liveRegion.setAttribute('role', 'status');
                liveRegion.setAttribute('aria-live', 'polite');
                container.appendChild(liveRegion);
              }
            }
            
            function updateLiveRegion(swiperInstance) {
              const container = swiperInstance.el;
              const liveRegion = container.querySelector('.swiper-live-region');
              const activeSlide = swiperInstance.slides[swiperInstance.activeIndex];
              
              if (liveRegion && activeSlide) {
                const content = activeSlide.innerText?.trim() || 'Slide changed';
                liveRegion.textContent = content;
              }
            }
            
            if (document.querySelector('.mcp-video-modal')) {
            const videoModals = document.querySelectorAll('.mcp-video-modal');
                videoModals.forEach((videoModal) => {
                      var openModalButton = videoModal.querySelector('.image-placeholder, .open-modal-button');
                      var modalConatiner = videoModal.querySelector('.modal-container');
                      var modalConatent= videoModal.querySelector('.modal-content');
                      var closeModalbutton = videoModal.querySelector('.close-modal');
                      var transcriptModalButton = videoModal.querySelector('.btn-video-transcript-modal-content');
                      var transcriptModalContent = videoModal.querySelector('.video-transcript-modal');
                      var videoPlayer = videoModal.querySelector('video');
                      var sliderWrapper = document.querySelector('.mcp-insights .swiper-wrapper');
                      let videoModalId = videoModal.querySelector('video').getAttribute('data-video-id');
                      saveStyle ="";
                      function trapFocus(modal) {
                        const firstFocusableElement = closeModalbutton;
                        const lastFocusableElement = videoModal.querySelector('.vjs-big-play-button');
                        const fullScreenButton = videoModal.querySelector('.vjs-fullscreen-control');
                        const playControl = videoModal.querySelector('.vjs-play-control');
                        const transcriptControl = videoModal.querySelector('.btn-video-transcript-modal-content');
                        
                        function handleTab(event) {
                          if (event.key === 'Tab') {                          
                            if (event.shiftKey) {
                              if (document.activeElement === lastFocusableElement) {
                                if(transcriptControl) {
                                  event.preventDefault();
                                  transcriptControl.focus({focusVisible: true});
                                } else {
                                  event.preventDefault();
                                  firstFocusableElement.focus({focusVisible: true});
                                } 
                              } else if (document.activeElement === firstFocusableElement) {
                                event.preventDefault();
                                lastFocusableElement.focus({focusVisible: true});
                              } else if (document.activeElement === fullScreenButton) {
                                event.preventDefault();
                                firstFocusableElement.focus({focusVisible: true});
                              } else if (document.activeElement === playControl) {
                                event.preventDefault();
                                firstFocusableElement.focus({focusVisible: true});
                              } else if(document.activeElement === transcriptControl) {
                                if (transcriptControl) {
                                  event.preventDefault();
                                  firstFocusableElement.focus({focusVisible: true});
                                }
                              }
                            } else {
                              if (document.activeElement === lastFocusableElement) {
                                if(transcriptControl) {
                                  event.preventDefault();
                                  transcriptControl.focus({focusVisible: true});
                                } else {
                                  event.preventDefault();
                                  firstFocusableElement.focus({focusVisible: true});
                                } 
                              } else if(document.activeElement === transcriptControl) {
                                if (transcriptControl) {
                                  event.preventDefault();
                                  firstFocusableElement.focus({focusVisible: true});
                                }
                              } else if (document.activeElement === fullScreenButton) {
                                event.preventDefault();
                                firstFocusableElement.focus({focusVisible: true});
                              }
                            }
                          } 
                          
                        }
                        modal.addEventListener('keydown', handleTab);
                      }
                      if (sliderWrapper) {
                        if (openModalButton && closeModalbutton) {
                          openModalButton.addEventListener("click", function (e) {
                              saveStyle = sliderWrapper.getAttribute('style');
                              document.body.classList.add('menu-is-active');
                              pauseAllVideos (videoModalId);
                              swiper.enabled = false;
                              if (saveStyle) {
                                  sliderWrapper.removeAttribute('style');
                              }

                          });
                          openModalButton.addEventListener('keydown', function(event) {
                            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                              openModalButton.click();
                            }
                          });
                          closeModalbutton.addEventListener("click", function () {
                            document.body.classList.remove('menu-is-active');
                            swiper.enabled = true;
                              if (saveStyle) {
                                  sliderWrapper.setAttribute('style');
                              }
                          });
                          closeModalbutton.addEventListener('keydown', function(event) {
                            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                              closeModalbutton.click();
                            }
                          });
                          document.addEventListener('keydown', function(event) {
                            if (event.key === 'Esc' || event.key === 'Escape') {
                              if (modalConatiner.classList.contains('show-modal')) {
                                saveStyle = sliderWrapper.getAttribute('style');
                                closeModalbutton.click();
                              }
                              
                            }
                          });
                          openModalButton.addEventListener("click", function () {
                            modalConatiner.classList.add('show-modal');
                          });
                          closeModalbutton.addEventListener("click", function () {
                              videoPlayer.pause();
                              modalConatiner.classList.remove('show-modal');
                          });
                          if (transcriptModalButton) {
                            transcriptModalButton.addEventListener("click", function () {
                              transcriptModalContent.classList.toggle('show-transcript');
                              modalConatent.classList.toggle('active-transcript');
                            });
                          }
                          
                          trapFocus(modalConatiner);
                        }
                          
                          
                          
                }
                    
                      
          
              });
          };

        }
}
document.querySelectorAll('.mcp-media-insights').forEach(mediaInsights => {
    const children = mediaInsights.querySelectorAll('.media-vertical-card');
    const mobileMenu = mediaInsights.querySelector('.mobile-menu');
    const menuList = document.createElement('ul');
    menuList.classList.add('mcp-menu-list');
    function stopPlayPodcast() {
        waitForElement('.brightcove-mcp-video-player').then((elm) => {
            children.forEach(podcast => {
                const podcastPlayer = podcast.querySelector('.mcp-podcast-container').querySelector('video');
                podcast.querySelector('.play-pause-btn').textContent = 'Listen now';
                podcast.querySelector('.play-pause-btn').classList.remove('playing');
                podcast.closest('.media-vertical-card').classList.remove('audio-playing');
                podcastPlayer.pause();
            });
        });
    }
if (children) {
    let activeDiv = null;
    children.forEach((child, index) => {
        children[0].classList.add('active');
        child.addEventListener('click', function() {
            const currentActive = mediaInsights.querySelector('.media-vertical-card.active');
            const currentIndex = Array.from(children).indexOf(currentActive);
            if (this === currentActive) {
                this.classList.remove('active');
                stopPlayPodcast();
                activeDiv = null; 
                return;
            }
    
            if (currentActive) {
                currentActive.classList.remove('active');
                stopPlayPodcast();
            }
    
            this.classList.add('active');
            const newIndex = index;
    
            if (currentIndex > newIndex) {
                this.classList.add('active');
            } else if (currentIndex < newIndex) {
                this.classList.add('active');
            } else {
                this.classList.add('active');
            }
    
            activeDiv = this;
            document.querySelectorAll('.transcription').forEach(transcription => {
                transcription.style.display = 'none';
            });

        });
        child.addEventListener('keydown', function(event) {
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                child.click();
            }
          });
        if(window.innerWidth <= 991 ) {
            const title = child.querySelector('.podcast-title');
            const menuItem = document.createElement('li');
            menuItem.setAttribute('data-id', index );
            if (title) {
                const titleContent = title.innerHTML;
                menuItem.textContent = titleContent;
                menuItem.classList.add('mcp-menu-item');
                
                if (index == 0) {
                    menuItem.classList.add('active');
                }
            }
            
            
            menuItem.addEventListener('click', function() {
                mobileLinks = mediaInsights.querySelectorAll('.mcp-menu-list li');
                mobileLinks.forEach((link, index) => {
                    link.classList.remove('active');
                });
                const dataId = parseInt(this.getAttribute('data-id'));
                this.classList.add('active');
                children.forEach(child => child.classList.remove('active'));
                const parent = document.querySelector('.swiper-wrapper');
                if (parent) {
                    if (dataId >= 0 && dataId < children.length) {
                        Array.from(children).forEach(child => child.classList.remove('active'));
                        children[dataId].classList.add('active');
                    }
                }
        
            });
            menuList.appendChild(menuItem);
            
        }
        
    });
    mobileMenu.appendChild(menuList);
}

});

currentlyPlayingVideo = null;
waitForElement('.brightcove-mcp-video-player').then((elm) => {
    document.querySelectorAll('.play-pause-btn').forEach(button => {
        button.classList.remove('hide');
        button.addEventListener('click', function(e) {
            e.stopPropagation();
            const video = this.closest('.mcp-podcast-container').querySelector('video');
            if (!video) return;
            if (currentlyPlayingVideo && currentlyPlayingVideo !== video) {
                currentlyPlayingVideo.pause();
                currentlyPlayingVideo.closest('.mcp-podcast-container').querySelector('.play-pause-btn').textContent = 'Listen now';
                currentlyPlayingVideo.closest('.mcp-podcast-container').querySelector('.play-pause-btn').classList.remove('playing');
                currentlyPlayingVideo.closest('.media-vertical-card').classList.remove('audio-playing');
            }
    
            if (video.paused) {
                video.play();
                currentlyPlayingVideo = video;
                this.textContent = 'Pause';
                this.classList.add('playing');
                currentlyPlayingVideo.closest('.media-vertical-card').classList.add('audio-playing');
            } else {
                video.pause();
                currentlyPlayingVideo = null;
                this.textContent = 'Listen now';
                this.classList.remove('playing');  
                elm.closest('.media-vertical-card').classList.remove('audio-playing');  
            }
        });
        button.addEventListener('keydown', function(event) {
            event.stopPropagation();
            if (event.key === 'Enter' || event.key === 13 || event.key === 32) {
                button.click();
            }
        });
    });
    
});



if (document.querySelector('.more-cards')) {
  const sections = document.querySelectorAll('.more-cards');
  sections.forEach((section) => {
    const disableCarousel = section.getAttribute("data-disable-carousel");
    const carouselItems = section.querySelectorAll('.more-cards .swiper-slide');
    const swiperPart = section.querySelector('.mcp-more-products__carousel');
    if (carouselItems.length > 1 && disableCarousel !== 'true') {
        if(window.innerWidth > 991 ) {
            const swiper = new Swiper(swiperPart, {
                slidesPerView: 'auto',
                spaceBetween: 0,
                slidesPerGroup: 1,
                loop: false,               
                pagination: {
                  el: '.swiper-pagination',
                  clickable: true,       
                },
                navigation: {
                  nextEl: '.swiper-button-next',
                  prevEl: '.swiper-button-prev',
                },
                a11y: {
                  scrollOnFocus: false,
                },
                on: {
                  init: function() {
                    updateAriaLabels (this);
                    setupLiveRegion(this);
                  },
                  slideChange: function() {
                    updateAriaLabels (this);
                    updateLiveRegion(this);
                  }
                }
              });
              function updateAriaLabels(swiperInstance) {
                const slides = swiperInstance.slides;
                const bullets = swiperInstance.pagination.bullets;
          
                bullets.forEach((bullet, index)=> {
                  const slide = slides[index];
                  const h3 = slide.querySelector('h3') || slide.querySelector('h4');
                  const title = h3 ? h3.textContent.trim() : '';
          
                  const baseLabel = `Go to slide ${index + 1}`;
                  const newLabel = title ? `${baseLabel}: ${title}` : baseLabel;
          
                  bullet.setAttribute('aria-label', newLabel);
                  });
              }
              function setupLiveRegion(swiperInstance) {
                const container = swiperInstance.el;
              
                // Check if live region already exists
                if (!container.querySelector('.swiper-live-region')) {
                  const liveRegion = document.createElement('div');
                  liveRegion.className = 'swiper-live-region visually-hidden';
                  liveRegion.setAttribute('role', 'status');
                  liveRegion.setAttribute('aria-live', 'polite');
                  container.appendChild(liveRegion);
                }
              }
              
              function updateLiveRegion(swiperInstance) {
                const container = swiperInstance.el;
                const liveRegion = container.querySelector('.swiper-live-region');
                const activeSlide = swiperInstance.slides[swiperInstance.activeIndex];
                
                if (liveRegion && activeSlide) {
                  const content = activeSlide.innerText?.trim() || 'Slide changed';
                  liveRegion.textContent = content;
                }
              }
        }
        
    }
  });
}


if (document.querySelector('.mcp-news-hub')) {
  const sections = document.querySelectorAll(".mcp-news-hub");
  sections.forEach((section) => {
    const carouselItems = section.querySelectorAll('.d .items .swiper-slide');
    if (carouselItems.length > 1) {
      const swiper = new Swiper('.mcp-news-hub .d .items', {
        slidesPerView: 1,
        spaceBetween: 0,
        slidesPerGroup: 1,
        loop: false,               
        pagination: {
          el: '.swiper-pagination',
          clickable: true,       
        },
        navigation: {
          nextEl: '.swiper-button-next',
          prevEl: '.swiper-button-prev',
        },
        on: {
          init: function() {
            updateAriaLabels (this);
            setupLiveRegion(this);
          },
          slideChange: function() {
            updateAriaLabels (this);
            updateLiveRegion(this);
          }
        }
      });
      function updateAriaLabels(swiperInstance) {
        const slides = swiperInstance.slides;
        const bullets = swiperInstance.pagination.bullets;

        bullets.forEach((bullet, index)=> {
          const slide = slides[index];
          const h3 = slide.querySelector('h3');
          const title = h3 ? h3.textContent.trim() : '';

          const baseLabel = `Go to slide ${index + 1}`;
          const newLabel = title ? `${baseLabel}: ${title}` : baseLabel;

          bullet.setAttribute('aria-label', newLabel);
          });
      }
      function setupLiveRegion(swiperInstance) {
        const container = swiperInstance.el;
      
        // Check if live region already exists
        if (!container.querySelector('.swiper-live-region')) {
          const liveRegion = document.createElement('div');
          liveRegion.className = 'swiper-live-region visually-hidden';
          liveRegion.setAttribute('role', 'status');
          liveRegion.setAttribute('aria-live', 'polite');
          container.appendChild(liveRegion);
        }
      }
      
      function updateLiveRegion(swiperInstance) {
        const container = swiperInstance.el;
        const liveRegion = container.querySelector('.swiper-live-region');
        const activeSlide = swiperInstance.slides[swiperInstance.activeIndex];
        
        if (liveRegion && activeSlide) {
          const content = activeSlide.innerText?.trim() || 'Slide changed';
          liveRegion.textContent = content;
        }
      }
      
    }
  });
}

if (document.querySelector('.highlight')) {
    const highlighContainer = document.querySelector('.highlight');
    highlighContainer.setAttribute('role', 'region');
    highlighContainer.setAttribute('aria-live', 'polite');
    highlighContainer.setAttribute('tabindex', '0');

}
document.addEventListener('DOMContentLoaded', function () {
    let transcriptOriginalLabel = null;
    let buttonOriginalLabel;
    document.body.addEventListener('click', function (e) {
      const button = e.target.closest('.btn-transcript-modal-content');
      if (!button) return;
      const targetId = button.getAttribute('data-modal-id');
      if (!targetId) return;
      const modals = document.querySelectorAll('.transcript-modal');
      if (document.querySelector('.transcript-modal')) {
        function modalFocus(modal) {
            const firstFocusableElement = modal.querySelector('.transcript-modal-content');
            const lastFocusableElement = modal.querySelector('.btn-transcript-close-modal');
            firstFocusableElement.focus({focusVisible: true});
            function handleTab(event) {
              if (event.key === 'Tab') {                          
                if (event.shiftKey) {
                  if (document.activeElement === lastFocusableElement) {
                    event.preventDefault();
                    firstFocusableElement.focus({focusVisible: true});
                  } else if (document.activeElement === firstFocusableElement) {
                    event.preventDefault();
                    lastFocusableElement.focus({focusVisible: true});
                  } 
                } else {
                    if (document.activeElement === firstFocusableElement) {
                        event.preventDefault();
                        lastFocusableElement.focus({focusVisible: true});
                    }
                    else if (document.activeElement === lastFocusableElement) {
                        event.preventDefault();
                        firstFocusableElement.focus({focusVisible: true});
                    }
                  
                }
              } 
              
            }
            modal.addEventListener('keydown', handleTab);
        }
      }
      modals.forEach(modal => {
        if (modal.getAttribute('data-modal-id') === targetId) {
          modal.classList.add('show-transcript');
          document.body.classList.add('menu-is-active');
        } else {
          modal.classList.remove('show-transcript');
        }
        modalFocus(modal)
        document.addEventListener('keydown', function(event) {
            if (event.key === 'Esc' || event.key === 'Escape') {
              if (modal.classList.contains('show-transcript')) {
                 modal.classList.remove('show-transcript');
                 document.body.classList.remove('menu-is-active');
              }
              
            }
        }); 
      });
      if (!transcriptOriginalLabel) {
        transcriptOriginalLabel = button.getAttribute('title') || button.getAttribute('aria-label');
      }
      if (!buttonOriginalLabel) {
        buttonOriginalLabel = button.textContent.trim();
        buttonChangedLabel = buttonOriginalLabel.replace('View', 'Hide');
      }
      this.classList.toggle('active');
      if (this.classList.contains('active')) {
        button.setAttribute('aria-label', 'Click here or press Enter to hide transcript');
        button.innerHTML = buttonChangedLabel;
      } 
    });
    document.querySelectorAll('.btn-transcript-close-modal').forEach(button => {
        button.addEventListener('click', function() {
            const modals = document.querySelectorAll('.transcript-modal');
            const buttonId = button.getAttribute('data-modal-id');
            modals.forEach(modal => {
                document.body.classList.remove('menu-is-active');
                modal.classList.remove('show-transcript');
            });
            const transcriptButtons = document.querySelectorAll('.btn-transcript-modal-content');
            transcriptButtons.forEach(button => {
                if (button.getAttribute('data-modal-id') === buttonId) {
                    button.setAttribute('aria-label', transcriptOriginalLabel);
                    button.innerHTML = buttonOriginalLabel;
                    transcriptOriginalLabel = null;
                } 
            });
        });
        button.addEventListener('keydown', function(event) {
            if (event.keyCode === 'Enter' || event.keyCode === 13 || event.keyCode === 32) {
                button.click();
            }
        }); 
    });
    
});

document.addEventListener("DOMContentLoaded", () => {
    function formatDate(inputDate) {
        const date = new Date(inputDate);
        const now = new Date();
        const diffTime = now - date;
        const diffDays = Math.floor(diffTime / (1000 * 60 * 60 * 24));
        if (diffDays >= 0 && diffDays <= 5) {
            return diffDays === 0 ? "Today" : `${diffDays} day${diffDays > 1 ? 's' : ''} ago`;
        }
        const options = { weekday: 'long', month: 'short', day: 'numeric', year: 'numeric' };
        return date.toLocaleDateString('en-US', options); 
    }

    async function getTranscriptFromCaption(src) {
         try {
            const response = await fetch(src);
            const data = await response.text();
            const cues = data.split(/\n\n+/);
            for (const cue of cues) {
                const lines = cue.split('\n');
                if (lines.length >= 2) {
                    const timeRange = lines[0];
                    const text = lines.slice(1).join(' ');
                    const [start, end] = timeRange.split(' --> ');
                            if (start.trim() === "03:33:33.333") {
                            return text;
                    }
                }       
            }

        } catch (error) {
            console.error('Error', error);
            return null;
        }
        
    }

    const containers = document.querySelectorAll(".brightcove-playlist");
    containers.forEach(container => {
        const desktop = parseInt(container.dataset.itemsDesktop);
        const tablet = parseInt(container.dataset.itemsTablet);
        const mobile = parseInt(container.dataset.itemsMobile);
        const limit = parseInt(container.dataset.playlistLimit);
        const accountId = container.dataset.playlistAccount;
        const playlistId = container.dataset.playlistId;
        const bcvPolicy = container.dataset.playlistToken;
        const mode = container.dataset.mode;
        const view = container.dataset.view;
        const bcplayer = container.dataset.player;
        const grid = container.querySelector(".playlist-grid");
        let showTranscript = container.dataset.transcript === 'true';
        
        

        container.querySelector(".playlist-grid").style.gridTemplateColumns = `repeat(${desktop}, 1fr)`;

        // Responsive logic
        const updateColumns = () => {
            const width = window.innerWidth;
            if (width < 768) {
                container.querySelector(".playlist-grid").style.gridTemplateColumns = `repeat(${mobile}, 1fr)`;
            } else if (width < 1024) {
                container.querySelector(".playlist-grid").style.gridTemplateColumns = `repeat(${tablet}, 1fr)`;
            } else {
                container.querySelector(".playlist-grid").style.gridTemplateColumns = `repeat(${desktop}, 1fr)`;
            }
        };
        const buildCards = () => {
            fetch("https://edge.api.brightcove.com/playback/v1/accounts/"+accountId+"/playlists/"+playlistId+"", {
                method: "GET",
                headers: {
                    "Accept": "application/json",
                    "BCOV-Policy": ""+bcvPolicy+""
                }
            })
            .then(response => response.json())
            .then(data =>  {
                let limitedData;
                if (limit == 0) {
                    limitedData = data.videos; 
                } else {
                    limitedData = data.videos.slice(0, limit); 
                }
                limitedData.forEach(async podcast => {
                    const formatedDate = formatDate(podcast.published_at);
                    const transcriptSrc = podcast?.text_tracks[0]?.src;
                    let transcript;
                    if (transcriptSrc) {
                        await getTranscriptFromCaption(transcriptSrc).then(text => {
                            transcript = text;
                        }) ;
                        
                    }
                    const html = `
                    <div class="media-card ${mode} ${view}">
                        <div class="media-card-image">
                            <img src="${podcast.poster}" role="presentation" alt="Poster for podcast: ${podcast.name}">
                        </div>
                        <div class="media-card-data">
                            <div class="media-card-data-container">
                                <div class="media-card-title"><h4>${podcast.name}</h4></div>
                                <div class="media-card-date">${formatedDate}</div>
                                <div class="media-card-description"><p>${podcast.description}</p></div>
                            </div>
                        <div class="media-card-btn-container">
                            <button class="media-card-btn-play" aria-label="Play podcast: ${podcast.name}" tabindex="0" data-podcast-id="${podcast.id}">Play podcast</button>
                            ${showTranscript && transcript != undefined ? `<button class="media-card-btn-transcript btn-transcript-modal-content" aria-label="Show or hide transcription" tabindex="0" data-modal-id="${podcast.id}">Show transcript</button>
                            <div class="transcript-modal" data-modal-id="${podcast.id}">
                                <div class="transcript-modal-container">
                                <div class="transcript-modal-header">
                                    <div class="transcription-title">Transcript</div>
                                    <button class="btn-transcript-close-modal" data-modal-id="${podcast.id}" type="button" aria-live="polite" aria-disabled="false" title="Close Transcription modal" tabindex="0"></button>
                                </div>
                                <div class="transcript-modal-content" role="region" aria-live="polite" tabindex="0" aria-label="Transcript section">
                                ${transcript}
                                </div>
                            </div>` : ''}
                        </div>
                        <div class="podcast-modal" data-podcast-id="${podcast.id}">
                            <div class="podcast-modal-container">
                                <div class="modal-header">
                                    <button class="close-modal" data-podcast-id="${podcast.id}" type="button" aria-live="polite" aria-disabled="false" title="Close podcast modal" tabindex="0"></button>
                                </div>
                                <div id="podcast-feed-${podcast.id}" class="podcast-container">
                                    <video data-video-id="${podcast.id}"
                                        data-account="${accountId}"
                                        data-player="${bcplayer}"
                                        data-embed="default"
                                        data-application-id
                                        class="video-js"
                                        controls>
                                    </video>
                                </div>

                        </div>
                        </div>
                        </div>
                        </div>
                    </div>      
                    `;
                    grid.insertAdjacentHTML('beforeend', html);
                });   
            })
            .catch(error => console.error("Error:", error));
        }
        function trapFocusPodcast(modal) {
            
            const firstFocusableElement = modal.querySelector('.vjs-big-play-button');
            const lastFocusableElement = modal.querySelector('.close-modal');
            const fullScreenButton = modal.querySelector('.vjs-fullscreen-control');
            const playControl = modal.querySelector('.vjs-play-control');
                                    
                function handleTab(event) {
                    if (event.key === 'Tab') {                          
                        if (event.shiftKey) {
                            if (document.activeElement === lastFocusableElement) {
                                event.preventDefault();
                                playControl.focus({focusVisible: true});
                            } else if (document.activeElement === firstFocusableElement) {
                                event.preventDefault();
                                lastFocusableElement.focus({focusVisible: true});
                            } else if (document.activeElement === fullScreenButton) {
                                event.preventDefault();
                                firstFocusableElement.focus({focusVisible: true});
                            } else if (document.activeElement === playControl) {
                                event.preventDefault();
                                if (window.getComputedStyle(firstFocusableElement).display !== 'none' ) {
                                    firstFocusableElement.focus({focusVisible: true});
                                } else {
                                    lastFocusableElement.focus({focusVisible: true});
                                }
                            } 
                        } else {
                            if (document.activeElement === lastFocusableElement) {
                                event.preventDefault();
                                if (window.getComputedStyle(firstFocusableElement).display !== 'none' ) {
                                    firstFocusableElement.focus({focusVisible: true});
                                } else {
                                    playControl.focus({focusVisible: true});
                                }                
                            } 
                            if (document.activeElement === fullScreenButton) {
                                event.preventDefault();
                                lastFocusableElement.focus({focusVisible: true});
                            }
                        }
                    }            
                }
                modal.addEventListener('keydown', handleTab);
        }
        window.addEventListener("resize", updateColumns);
        updateColumns();
        buildCards();
        setTimeout(() => {
            if (window.bc && window.videojs) {
            const allNewPodcasts = container.querySelectorAll('video[data-account]');
            allNewPodcasts.forEach(el => {
                bc(el);
                 
            });
            }
            const podcastsModals = document.querySelectorAll('.podcast-modal');
                podcastsModals.forEach((podcastModal) => {
                      trapFocusPodcast(podcastModal)
                });
        }, 500);
    });
    document.body.addEventListener('click', function (event) {
        if (event.target.classList.contains('btn-transcript-close-modal')) {
            const button = event.target;
            const buttonId = button.getAttribute('data-modal-id');
            const modals = document.querySelectorAll('.transcript-modal');
            modals.forEach(modal => {
                document.body.classList.remove('menu-is-active');
                modal.classList.remove('show-transcript');
            });
            const transcriptButtons = document.querySelectorAll('.btn-transcript-modal-content');
            let transcriptOriginalLabel = null;
            transcriptButtons.forEach(transcriptButton => {
                if (!transcriptOriginalLabel) {
                    transcriptOriginalLabel = button.getAttribute('title') || button.getAttribute('aria-label');
                }
                if (transcriptButton.getAttribute('data-modal-id') === buttonId) {
                    transcriptButton.setAttribute('aria-label', transcriptOriginalLabel);
                    transcriptOriginalLabel = null;
                    buttonOriginalLabel = null;
                }
            });
        }
        if (event.target.classList.contains('media-card-btn-play')) {
            const buttonPlay = event.target;
            const buttonPlayId = buttonPlay.getAttribute('data-podcast-id');
            const podcasts = document.querySelectorAll('.podcast-modal');
            podcasts.forEach(podcast => {
                if (podcast.getAttribute('data-podcast-id') === buttonPlayId) {
                    podcast.classList.add('show-modal');
                    podcast.querySelector('.vjs-big-play-button').focus({focusVisible: true});
                    document.body.classList.add('menu-is-active');
                } else {
                    podcast.classList.remove('show-modal');
                }
           
            });
        }
        if (event.target.classList.contains('close-modal')) {
            const modals = document.querySelectorAll('.podcast-modal');
            document.querySelectorAll('video').forEach(function(podcast) {
                if (podcast.getAttribute("data-video-id")) {
                    podcast.pause();
                    const playButtons = document.querySelectorAll('.play-button');
                    playButtons.forEach((playButton) => {
                        playButton.classList.remove('playing');
                    });
                }
            });
            modals.forEach(modal => {
                document.body.classList.remove('menu-is-active');
                modal.classList.remove('show-modal');
            });
        }
    });

});


function setActive(id) {
    const elem = document.querySelector(`[href="${id}"]`);
    if (!elem) return;
    const items = elem.parentElement.querySelectorAll('.jumplink');
    items.forEach(item => item.classList.remove('active'));
    elem.classList.add('active');
}


window.addEventListener('scroll', () => {
    const jlinks = document.querySelectorAll('.jumplink');
    if (jlinks.length) {
        const currentScroll = window.scrollY;
        let closestLink = null;
        let closestDistance = Infinity;
        jlinks.forEach(link => {
            const id = link.getAttribute('href');
            const el = document.querySelector(id);
            if (el) {
                const elementTop = el.getBoundingClientRect().top + window.scrollY + 200;
                const distance = Math.abs(currentScroll - elementTop);
                if (distance < closestDistance) {
                    closestDistance = distance;
                    closestLink = id;
                }
            }
        });
        if (closestLink) {
            setActive(closestLink);
        }
    }
});


document.addEventListener('DOMContentLoaded', () => {
    const jlinks = document.querySelectorAll('.jumplink');
    if (jlinks.length) {
        jlinks.forEach(link => {
            // Set id and aria-label based on anchor text
            const anchorText = link.textContent.trim();
            const idValue = anchorText.toLowerCase().replace(/\s+/g, '-') + '-tab';
            link.setAttribute('id', idValue);
            link.setAttribute('aria-label', 'Click ' + anchorText.toLowerCase() + ' tab');

            link.addEventListener('click', function() {
                const id = this.getAttribute('href');
                setActive(id);
            });
        });
    }
    
});
document.querySelectorAll('#footer-navigation .cmp-navigation__item--level-0').forEach(topLevelItem => {
    const link = topLevelItem.querySelector('a.cmp-navigation__item-link');
    const subNav = topLevelItem.querySelector('ul.cmp-navigation__group');
  
    if (link) {
      const linkText = link.textContent.trim();
      const hasSubnav = subNav !== null;
      const desc = `Click or press Enter to navigate to the ${linkText} page${hasSubnav ? '. This section has sub-navigation.' : ''}`;
      link.setAttribute('title', desc);
      link.setAttribute('aria-label', desc);

      if (subNav) {
        subNav.querySelectorAll('a.cmp-navigation__item-link').forEach(subLink => {
          const subText = subLink.textContent.trim();
          const subDesc = `Click or press Enter to navigate to the ${subText} page. This page is part of ${linkText} section`;
          subLink.setAttribute('title', subDesc);
          subLink.setAttribute('aria-label', subDesc);
        });
      }
    }

});
  
