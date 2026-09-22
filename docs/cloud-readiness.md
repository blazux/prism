# Préparation de Prism à la bêta Cloud

Ordre demandé : compléter les fonctionnalités de Prism, puis écrire la recette d'exploitation dans Prism Cloud, puis seulement envisager le déploiement. Aucun déploiement local ou VPS ne fait partie de ces modifications de code.

## Configuration IA du produit Prism

Implémenté dans le code et couvert par des tests ; utilisé et déployé sur l’instance locale. Cela ne valide pas le BYOK Cloud personnel :

- Settings → AI provider en mono-user ; Admin → AI provider pour l’administrateur global en multi-user. OpenAI/Anthropic avec URL préconfigurée, Ollama et Other compatible avec URL explicite.
- Profil de déploiement enregistré atomiquement dans un secret chiffré.
- En multi-user, une configuration administrateur commune ; aucune configuration IA dans les settings utilisateurs. Sans override, paramètres `.env` conservés.
- Application aux conversations WebSocket, au chat sans navigateur, aux actions IA des applications et aux appels d'outils HTTP utilisant un modèle.
- Changement pris en compte au prochain message d'une conversation ouverte, sans perdre l'historique ni interrompre un tour en cours.
- Outil existant `agent_settings` étendu, documentation embarquée `ai-provider`, état de configuration disponible pour l'agent.
- Clés exclues des réponses de configuration et des secrets injectés aux scripts. Un secret de script utilisé explicitement comme source reste un secret de script.
- Les restrictions administrateur sur les modèles sont conservées ; toutes les modifications IA sont réservées à l’administrateur global en multi-user.

Les agents de groupe utilisent la même configuration de déploiement. Embeddings configurables via la même connexion ou une connexion indépendante, test réel de dimension, application à chaud avec progression. Changement de modèle/endpoint : accord explicite et réindexation transactionnelle des textes stockés à la sauvegarde, après la fin des opérations en cours. La table `rag_embedding_meta` mémorise l’identité de l’index, y compris à dimension égale. Le repli visuel pour les widgets suit le modèle de conversation déclaré compatible vision ; les déploiements sans override conservent `VISION_MODEL`.

Ce réglage global du mode multi-user existant ne représente pas le BYOK par compte de PrismCloud. La résolution d’un profil par propriétaire Cloud et l’isolation de ses ressources restent à concevoir et valider sans transformer les groupes entreprise en comptes Cloud.

## Lots suivants, nécessaires avant la recette exécutable

1. **Embeddings Cloud** : la configuration et la migration locale sont implémentées ; coordonner les répliques, l’indisponibilité et les travaux de fond pour une migration Cloud. La bêta prévoit un profil commun, pas un modèle d’embeddings différent par testeur.
2. **Frontière du workspace** : introduire la résolution propriétaire → environnement d'exécution, vérifier montages, fichiers, outils, secrets, crons et services. Préserver les primitives génériques et les modes locaux.
3. **Isolation des données et authentification bêta** : valider les propriétaires sur chaque parcours, invitations, révocation et récupération ; ne pas assimiler les groupes existants à une isolation SaaS déjà démontrée.
4. **Deux répliques** : sessions partagées, propriété des tours, événements et éditeurs, annulation et reconnexion ; définir les effets incertains après panne.
5. **Travaux de fond** : réservation et reprise des ingestions/synchronisations, propriétaires uniques des canaux et workspaces, comportement des crons après arrêt.
6. **Contrat de réutilisation Prism/Cloud** : valider le partage effectif du produit et de ses ressources web avant de figer le packaging.

## Après ces lots

Écrire la recette dans Prism Cloud avec versions épinglées, réseaux, volumes, limites, migrations, sauvegardes/restauration et tests d'acceptation. La tester dans un environnement isolé avant toute installation sur le VPS.

Abonnements et facturation exclus de la bêta. Ce premier lot ne constitue ni une architecture distribuée achevée ni une validation d'isolation pour une ouverture publique.

## État des lieux du 22 septembre 2026

La [checklist alpha](../../Prism%20Cloud/docs/09-alpha-readiness.md) relie le code actuel aux critères d’ouverture et fixe la première tranche : deux propriétaires dans un même serveur mutualisé. Les correctifs de sécurité locaux sont testés et déployés ; la résolution Cloud, le provisionnement et la coordination distribuée restent à implémenter. Aucun déploiement Cloud n’a été réalisé.
