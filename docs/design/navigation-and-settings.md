# Application Navigation & Settings Design Principles

**Document Type:** UX / Information Architecture Standards  
**Status:** Design Standard  
**Version:** 1.0

## 1. Purpose

This document establishes consistent design principles for organizing application navigation, menus, settings, and configuration interfaces.

The objective is to create an intuitive, predictable, discoverable, and scalable application experience that accommodates both novice and experienced users.

All application features, navigation elements, and settings should follow these principles to ensure consistency as the application evolves.

The guiding principle is:

> Organize the application around how users think about and interact with functionality, not how the underlying software is implemented.

## 2. Core Design Principles

### 2.1 Information Architecture

Information architecture (IA) defines how application functionality is organized, categorized, and presented.

All application functionality must have a logical and predictable location.

Apply the following principles:

- **Logical Grouping:** Group functionality according to its purpose and relationship to other features.
- **Mutual Exclusivity:** Categories should have clearly distinguishable responsibilities. Avoid placing the same setting in multiple unrelated locations.
- **Predictability:** Users should be able to anticipate where functionality will be located.
- **Discoverability:** Important functionality should be accessible without navigating excessive levels of menus.
- **Scalability:** Categories should accommodate future functionality without requiring fundamental navigation restructuring.
- **Consistency:** Similar functionality should follow the same organizational patterns throughout the application.

Avoid creating categories solely because they are convenient from an implementation or development perspective.

### 2.2 Hick’s Law: Minimize Cognitive Load

Hick’s Law describes the relationship between the number of available choices and the time required to make a decision.

Applications should minimize unnecessary decision complexity.

Guidelines:

- Keep primary navigation focused on the most important application destinations.
- Avoid presenting excessive unrelated options in a single menu.
- Group related functionality into meaningful categories.
- Use submenus when they improve organization and comprehension.
- Prefer progressive disclosure over presenting every available option simultaneously.
- Avoid unnecessary navigation depth.

As a general guideline, aim for approximately 5–7 primary navigation destinations. This is a starting point, not a strict requirement.

The objective is to reduce cognitive load without unnecessarily restricting functionality.

### 2.3 Jakob’s Law: Follow Established Conventions

Users bring expectations from applications they already understand.

Application navigation should follow established industry and platform conventions whenever practical.

Examples:

| Functionality | Expected Location |
| --- | --- |
| User profile | Account / Profile |
| Theme selection | Appearance |
| Notifications | Notifications |
| Password and authentication | Security |
| API keys | Developer / Integrations |
| Application version | About |
| Keyboard shortcuts | Shortcuts |
| Data export | Privacy / Data |
| External services | Connections / Integrations |

Avoid introducing novel terminology, navigation patterns, or organizational structures without a meaningful usability benefit.

### 2.4 Progressive Disclosure

Applications frequently serve users with different levels of experience.

The interface should accommodate both simple and advanced workflows without overwhelming either audience.

Guidelines:

- Present commonly used settings prominently.
- Group specialized settings under appropriate advanced sections.
- Use expandable sections for optional configuration.
- Provide contextual explanations for complex settings.
- Avoid exposing implementation details unless they are relevant to the user.
- Ensure advanced functionality remains accessible rather than removing it.

Advanced settings should be discoverable but should not dominate the default experience.

### 2.5 Separation of Navigation and Configuration

Navigation and configuration serve different purposes and should remain conceptually distinct.

Navigation answers: *What can I do?*

Settings answer: *How does the application behave?*

Examples:

| Navigation | Corresponding Configuration |
| --- | --- |
| Create project | Default project behavior |
| Start conversation | Default conversation settings |
| Browse models | Model preferences |
| View activity | Activity retention |
| Manage integrations | Integration configuration |

Do not use the primary navigation structure as a dumping ground for configuration options.

## 3. Application Navigation Architecture

Application navigation should prioritize the primary workflows users perform.

A recommended high-level structure follows.

### 3.1 Primary Navigation

Primary navigation contains the application’s most important destinations.

Examples:

- Home / Dashboard
- Main application functionality
- Projects / Workspaces
- History / Activity
- Resources

Only destinations that represent meaningful application workflows should appear in primary navigation.

### 3.2 Application Settings

Application settings control global application behavior and presentation.

Recommended categories:

| Category | Responsibilities |
| --- | --- |
| General | Default behavior and application preferences |
| Appearance | Themes, layouts, visual preferences |
| Notifications | Notification behavior and preferences |
| Privacy & Security | Permissions, authentication, data handling |
| Connections / Integrations | External services and integrations |
| Advanced | Specialized application configuration |
| About | Version information, licenses, documentation |

Categories should only be included when the application has relevant functionality.

### 3.3 Account Settings

Account settings control identity, access, and account-level preferences.

Recommended categories:

- Profile
- Authentication
- Subscription / Billing
- Account management
- Personal preferences

Account settings should remain separate from application-wide configuration, particularly when multiple users or accounts are supported.

### 3.4 Contextual Settings

Some settings apply only to a specific resource or workflow.

Examples:

- Project-specific configuration
- Workspace preferences
- Individual resource configuration
- Conversation-specific settings
- Model-specific configuration

Contextual settings should generally be accessible from the resource they affect.

Avoid placing every resource-specific setting in the global settings interface.

## 4. Settings Classification

Every setting should have an identifiable scope and complexity classification.

### 4.1 Scope

Settings should be classified according to their effect.

| Scope | Definition |
| --- | --- |
| Account | Applies to an individual user’s account |
| Application | Applies globally to the application |
| Workspace | Applies to a specific workspace |
| Project | Applies to a specific project |
| Resource | Applies to an individual resource or object |

A setting’s location should reflect its scope.

For example, a global model preference belongs in application settings, while an individual project’s model override belongs in project configuration.

### 4.2 Complexity

Settings should also be classified according to their intended audience.

| Classification | Description |
| --- | --- |
| Basic | Commonly used settings appropriate for most users |
| Advanced | Specialized settings requiring additional knowledge |
| Developer | Settings primarily intended for development, debugging, or integration |
| Administrative | Settings governing application management, users, or organizational policies |

These classifications should guide presentation and discoverability, not unnecessarily restrict access.

## 5. Menu and Navigation Design

### 5.1 Naming Conventions

Navigation labels should be:

- Short and descriptive.
- Written in familiar language.
- Consistent throughout the application.
- Based on user-facing concepts rather than internal implementation terminology.

Prefer:

- *Appearance* instead of *Visual Configuration*.
- *Connections* instead of *External Service Management*.
- *General* instead of *Miscellaneous*.
- *Security* instead of *Authentication Infrastructure*.

Avoid ambiguous labels such as:

- Other
- Miscellaneous
- Advanced Options (when the contents are unrelated)
- Configuration (when a more descriptive category exists)

### 5.2 Navigation Depth

Navigation should remain as shallow as practical.

Guidelines:

- Prefer direct access to frequently used destinations.
- Avoid deeply nested menu structures.
- Use secondary navigation for meaningful subdivisions.
- Avoid creating a category containing only one setting unless there is a compelling organizational reason.
- Preserve clear navigation context when users enter nested settings.

### 5.3 Visual Consistency

Navigation elements should use consistent:

- Icons and iconography.
- Typography.
- Spacing.
- Alignment.
- Selection states.
- Hover and focus states.
- Interaction behavior.

Icons should reinforce textual labels rather than replace them when the meaning is not universally recognizable.

### 5.4 Responsive Behavior

Navigation should adapt to the target platform and available screen space.

Examples:

- **Desktop:** Persistent sidebars or structured navigation panels.
- **Mobile:** Hierarchical navigation, grouped lists, or platform-native navigation patterns.
- **Tablet:** Adaptive layouts that balance persistent navigation and content space.

Follow established platform conventions rather than forcing identical navigation layouts across fundamentally different form factors.

## 6. Settings Presentation

### 6.1 Group Related Controls

Settings within a category should be grouped according to their purpose.

For example, Appearance might contain:

- Theme
- Color scheme
- Font size
- Density
- Layout preferences

Avoid mixing unrelated controls in the same section.

### 6.2 Provide Context

Complex settings should include concise descriptions explaining:

- What the setting controls.
- What changing it affects.
- Whether it has dependencies.
- Whether the change is immediate or requires confirmation.

Avoid descriptions that merely repeat the setting’s label.

### 6.3 Defaults

Settings should have sensible defaults.

Guidelines:

- Prefer defaults that work for the majority of users.
- Avoid requiring users to configure the application before using its primary functionality.
- Clearly distinguish default values from user-customized values when appropriate.
- Provide a way to restore defaults where practical.

### 6.4 Destructive and High-Impact Settings

Settings that can cause data loss, security changes, or significant behavioral changes should receive additional consideration.

Examples:

- Delete account
- Reset application data
- Remove integrations
- Disable security protections
- Clear application history

Use confirmation mechanisms and clear descriptions of consequences.

Do not place destructive actions alongside ordinary preference controls without visual distinction.

## 7. Platform Design Standards

The application should respect the design conventions of its target platforms.

Relevant references:

- Apple Human Interface Guidelines: <https://developer.apple.com/design/human-interface-guidelines/>
- Material Design 3: <https://m3.material.io/>
- Nielsen Norman Group UX Research: <https://www.nngroup.com/articles/>

Platform conventions should inform:

- Navigation patterns.
- Control behavior.
- Accessibility.
- Typography.
- Interaction feedback.
- Responsive layouts.

Consistency with platform conventions should take precedence over unnecessary custom interface patterns.

## 8. Accessibility and Usability

All navigation and settings interfaces should be designed with accessibility in mind.

Requirements:

- Support keyboard navigation where applicable.
- Provide visible focus indicators.
- Use accessible labels for interactive controls.
- Do not rely exclusively on color to communicate state.
- Maintain sufficient contrast.
- Ensure controls have understandable names.
- Provide meaningful feedback when settings change.
- Respect platform accessibility preferences.

The interface should remain understandable and usable for people with different levels of technical experience and accessibility needs.

## 9. Feature Placement Guidelines

Every new feature must be evaluated before being assigned a navigation location.

The following questions should guide placement:

1. Is this a primary application workflow or a configuration option?
2. Does it affect the entire application, an account, a workspace, a project, or an individual resource?
3. Is it commonly used or intended for advanced users?
4. Does it belong to an existing category?
5. Would users naturally look for it in the proposed location?
6. Does its placement create unnecessary duplication?
7. Will this organizational decision remain reasonable as functionality expands?

New functionality should extend existing categories whenever possible rather than creating additional top-level navigation destinations.

New categories should be introduced only when they represent a meaningful and distinct functional grouping.

## 10. Settings and Navigation Inventory

Maintain a centralized inventory of application settings.

Each setting should document:

| Attribute | Description |
| --- | --- |
| Name | User-facing setting name |
| Description | What the setting controls |
| Category | Navigation or settings category |
| Scope | Account, application, workspace, project, or resource |
| Complexity | Basic, advanced, developer, or administrative |
| Default | Default configuration value |
| Dependencies | Related settings or prerequisites |
| Impact | Behavioral, visual, security, or data impact |

This inventory serves as the source of truth for settings organization and helps prevent configuration sprawl.

Box Office's inventory is [settings-inventory.md](settings-inventory.md). The first audit against this standard is [ui-audit-2026-11.md](ui-audit-2026-11.md).

## 11. Design Review Checklist

Before approving new navigation or settings functionality, evaluate it against the following checklist.

- [ ] The functionality has a clearly defined purpose.
- [ ] Its placement reflects how users understand the functionality.
- [ ] It belongs to an appropriate existing category or a justified new category.
- [ ] Its scope is correctly identified.
- [ ] Its complexity classification is appropriate.
- [ ] It follows established platform conventions.
- [ ] It does not unnecessarily increase navigation complexity.
- [ ] Related settings are grouped logically.
- [ ] Labels use consistent, familiar terminology.
- [ ] Advanced functionality does not overwhelm basic workflows.
- [ ] Accessibility requirements are satisfied.
- [ ] Destructive actions are clearly distinguished.
- [ ] The organizational structure can accommodate future functionality.

## 12. Guiding Principles Summary

The following principles should guide all navigation and settings decisions:

1. **User-Centered Organization:** Organize around user mental models, not implementation details.
2. **Logical Grouping:** Related functionality belongs together.
3. **Predictability:** Users should know where to find functionality.
4. **Minimal Cognitive Load:** Avoid unnecessary choices and complexity.
5. **Established Conventions:** Follow familiar UX and platform patterns.
6. **Progressive Disclosure:** Keep common functionality accessible and advanced functionality discoverable.
7. **Separation of Concerns:** Distinguish application navigation, account configuration, and contextual settings.
8. **Consistent Terminology:** Use clear and familiar language throughout the application.
9. **Scalability:** Design the information architecture to accommodate growth.
10. **Accessibility:** Ensure functionality is usable by the widest practical audience.

**Final Standard:** Every navigation element and setting must have a deliberate, defensible location within the application’s information architecture. Organizational decisions should improve usability, discoverability, and consistency rather than reflect internal software boundaries.
